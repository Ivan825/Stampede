package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/ai"
	"github.com/Ivan825/Stampede/internal/ai/provider"
	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/keyring"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
)

// AIConfig configures optional AI journey generation. Providers and keys
// are configured per organisation through the API; nothing here is needed
// for the feature to work.
type AIConfig struct {
	// NewProvider builds a provider client (tests inject fakes). The
	// default is provider.New.
	NewProvider func(provider.Config) (provider.Provider, error)
	// Workers bounds concurrently running jobs (default 2).
	Workers int
	// MaxQueued bounds jobs waiting for a worker (default 20).
	MaxQueued int
	// DefaultMonthlyTokenCap applies to providers saved without a cap
	// (default 2,000,000 tokens).
	DefaultMonthlyTokenCap int64
	// JobTimeout bounds one job (default 30 minutes).
	JobTimeout time.Duration
	// Transport overrides the dry run's HTTP transport (tests).
	Transport http.RoundTripper
}

// AI job statuses, as stored.
const (
	aiQueued      = "queued"
	aiRunning     = "running"
	aiSucceeded   = "succeeded"
	aiNeedsReview = "needs_review"
	aiFailed      = "failed"
)

// Input size limits for one job.
const (
	maxAIDescription = 20_000
	maxAISpec        = 5 << 20
	maxAITraffic     = 20 << 20
)

// aiManager runs generation jobs in a bounded worker pool. Jobs live only
// in this process; a restart marks unfinished ones failed.
type aiManager struct {
	s      *Server
	sem    chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	queued atomic.Int64
}

func newAIManager(s *Server) *aiManager {
	c := &s.cfg.AI
	if c.NewProvider == nil {
		c.NewProvider = provider.New
	}
	if c.Workers <= 0 {
		c.Workers = 2
	}
	if c.MaxQueued <= 0 {
		c.MaxQueued = 20
	}
	if c.DefaultMonthlyTokenCap <= 0 {
		c.DefaultMonthlyTokenCap = 2_000_000
	}
	if c.JobTimeout <= 0 {
		c.JobTimeout = 30 * time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &aiManager{s: s, sem: make(chan struct{}, c.Workers), ctx: ctx, cancel: cancel}
}

func (m *aiManager) recover(ctx context.Context) error {
	n, err := m.s.st.FailOrphanedAIJobs(ctx, m.s.cfg.Now().Add(-m.s.cfg.ReplicaStale))
	if err != nil {
		return err
	}
	if n > 0 {
		m.s.log.Warn("marked interrupted AI generation jobs as failed", "count", n)
	}
	return nil
}

// shutdown cancels running jobs and waits for them to record their end.
func (m *aiManager) shutdown(ctx context.Context) {
	m.cancel()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

type aiJobSpec struct {
	id     uuid.UUID
	inputs ai.Inputs
	opts   ai.Options
}

func (m *aiManager) launch(spec aiJobSpec) {
	m.wg.Add(1)
	m.queued.Add(1)
	go func() {
		defer m.wg.Done()
		bg := context.WithoutCancel(m.ctx)
		select {
		case m.sem <- struct{}{}:
			m.queued.Add(-1)
		case <-m.ctx.Done():
			m.queued.Add(-1)
			m.record(bg, spec.id, "", nil, errors.New("interrupted: the server is shutting down"))
			return
		}
		defer func() { <-m.sem }()
		ctx, cancel := context.WithTimeout(m.ctx, m.s.cfg.AI.JobTimeout)
		defer cancel()
		if err := m.s.st.StartAIJob(bg, spec.id); err != nil {
			m.s.log.Error("AI job start", "job", spec.id, "error", err)
		}
		stage := ""
		spec.opts.Progress = func(p ai.Progress) {
			stage = p.Stage
			err := m.s.st.UpdateAIJobProgress(bg, db.UpdateAIJobProgressParams{
				ID: spec.id, Stage: p.Stage, Round: int32(p.Round), //nolint:gosec // a handful of rounds
				InputTokens: p.Usage.InputTokens, OutputTokens: p.Usage.OutputTokens,
			})
			if err != nil {
				m.s.log.Error("AI job progress", "job", spec.id, "error", err)
			}
		}
		res, err := ai.Generate(ctx, spec.inputs, spec.opts)
		if err != nil && ctx.Err() != nil && m.ctx.Err() != nil {
			err = errors.New("interrupted: the server is shutting down")
		} else if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("the job took longer than %s", m.s.cfg.AI.JobTimeout)
		}
		m.record(bg, spec.id, stage, res, err)
	}()
}

// aiResult is the stored form of a job's outcome.
type aiResult struct {
	Journeys     []ai.JourneyResult `json:"journeys"`
	Problems     []ai.Problem       `json:"problems"`
	Endpoints    int                `json:"endpoints,omitempty"`
	Dependencies []ai.Dependency    `json:"dependencies,omitempty"`
}

func (m *aiManager) record(ctx context.Context, id uuid.UUID, stage string, res *ai.Result, runErr error) {
	p := db.FinishAIJobParams{ID: id, Stage: stage, Status: aiFailed, Result: []byte("{}")}
	if res != nil {
		p.Round = int32(res.Rounds) //nolint:gosec // a handful of rounds
		p.Yaml, p.Diff = res.YAML, res.Diff
		p.InputTokens, p.OutputTokens = res.Usage.InputTokens, res.Usage.OutputTokens
		out := aiResult{Journeys: res.Journeys, Problems: res.Problems}
		if u := res.Understanding; u != nil {
			out.Endpoints, out.Dependencies = len(u.Endpoints), u.Dependencies
		}
		if b, err := json.Marshal(out); err == nil {
			p.Result = b
		}
	}
	switch {
	case runErr != nil:
		p.Error = runErr.Error()
	case res.Fatal():
		var msgs []string
		for _, pr := range res.Problems {
			if pr.Fatal {
				msgs = append(msgs, pr.String())
			}
		}
		p.Error = "no usable scenario was produced: " + strings.Join(msgs, "; ")
	case res.Validated():
		p.Status, p.Stage = aiSucceeded, ai.StageDone
	default:
		p.Status, p.Stage = aiNeedsReview, ai.StageDone
	}
	if err := m.s.st.FinishAIJob(ctx, p); err != nil {
		m.s.log.Error("AI job finish", "job", id, "error", err)
	}
}

// Providers.

func aiProviderAAD(org, id uuid.UUID) []byte {
	return []byte("stampede-ai-provider:" + org.String() + ":" + id.String())
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func (h *handlers) aiTokensThisMonth(ctx context.Context, org uuid.UUID) (int64, error) {
	return h.st.AITokensSince(ctx, db.AITokensSinceParams{OrgID: org, CreatedAt: monthStart(h.cfg.Now())})
}

func aiProviderOf(p db.AiProvider, used int64) gen.AIProvider {
	out := gen.AIProvider{
		Id: p.ID, Name: p.Name, Kind: gen.AIProviderKind(p.Kind), Model: p.Model, HasKey: len(p.Ciphertext) > 0,
		MonthlyTokenCap: p.MonthlyTokenCap, UsedTokensThisMonth: used, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
	if p.BaseUrl != "" {
		b := p.BaseUrl
		out.BaseURL = &b
	}
	return out
}

func (h *handlers) ListAIProviders(ctx context.Context, _ gen.ListAIProvidersRequestObject) (gen.ListAIProvidersResponseObject, error) {
	p, err := need(ctx, auth.PermView)
	if err != nil {
		return nil, err
	}
	rows, err := h.st.ListAIProviders(ctx, p.OrgID)
	if err != nil {
		return nil, err
	}
	used, err := h.aiTokensThisMonth(ctx, p.OrgID)
	if err != nil {
		return nil, err
	}
	out := gen.ListAIProviders200JSONResponse{}
	for _, r := range rows {
		out = append(out, aiProviderOf(r, used))
	}
	return out, nil
}

func (h *handlers) PutAIProvider(ctx context.Context, req gen.PutAIProviderRequestObject) (gen.PutAIProviderResponseObject, error) {
	p, err := need(ctx, auth.PermManageUsers)
	if err != nil {
		return nil, err
	}
	b := req.Body
	kind, err := provider.ParseKind(string(b.Kind))
	if err != nil {
		return nil, errInvalid(err.Error())
	}
	name := "default"
	if b.Name != nil {
		name = strings.TrimSpace(*b.Name)
	}
	if name == "" || len(name) > 100 {
		return nil, errInvalid("name must be 1 to 100 characters")
	}
	model := ""
	if b.Model != nil {
		model = strings.TrimSpace(*b.Model)
	}
	if model == "" {
		model = provider.DefaultModel(kind)
	}
	if model == "" {
		return nil, errInvalid(fmt.Sprintf("model is required for %s", kind))
	}
	base := ""
	if b.BaseURL != nil {
		base = strings.TrimRight(strings.TrimSpace(*b.BaseURL), "/")
	}
	if base != "" {
		u, err := url.Parse(base)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, errInvalid("baseURL must be an absolute http(s) URL")
		}
	}
	if kind == provider.OpenAICompatible && base == "" {
		return nil, errInvalid("baseURL is required for openai-compatible providers, for example http://llm.internal:8000/v1")
	}
	monthlyCap := h.cfg.AI.DefaultMonthlyTokenCap
	if b.MonthlyTokenCap != nil {
		if *b.MonthlyTokenCap <= 0 {
			return nil, errInvalid("monthlyTokenCap must be positive")
		}
		monthlyCap = *b.MonthlyTokenCap
	}

	existing, err := h.st.GetAIProviderByName(ctx, db.GetAIProviderByNameParams{OrgID: p.OrgID, Name: name})
	exists := err == nil
	if err != nil && !store.IsNotFound(err) {
		return nil, err
	}
	id := uuid.New()
	var sealed keyring.Sealed
	if exists {
		id = existing.ID
		sealed = keyring.Sealed{Ciphertext: existing.Ciphertext, WrappedKey: existing.WrappedKey}
		if existing.KeyID != nil {
			sealed.KeyID = *existing.KeyID
		}
	}
	if b.ApiKey != nil && strings.TrimSpace(*b.ApiKey) != "" {
		kr, err := h.keyring()
		if err != nil {
			return nil, errConflict("AI provider keys are stored encrypted: start the server with STAMPEDE_MASTER_KEY set")
		}
		if sealed, err = kr.Seal([]byte(strings.TrimSpace(*b.ApiKey)), aiProviderAAD(p.OrgID, id)); err != nil {
			return nil, err
		}
	}
	if provider.NeedsKey(kind) && len(sealed.Ciphertext) == 0 {
		return nil, errInvalid(fmt.Sprintf("apiKey is required for %s", kind))
	}
	var keyID *string
	if sealed.KeyID != "" {
		k := sealed.KeyID
		keyID = &k
	}
	uid := p.UserID
	if exists {
		err = h.st.UpdateAIProvider(ctx, db.UpdateAIProviderParams{
			ID: id, OrgID: p.OrgID, Kind: string(kind), Model: model, BaseUrl: base,
			Ciphertext: sealed.Ciphertext, WrappedKey: sealed.WrappedKey, KeyID: keyID, MonthlyTokenCap: monthlyCap,
		})
	} else {
		err = h.st.CreateAIProvider(ctx, db.CreateAIProviderParams{
			ID: id, OrgID: p.OrgID, Name: name, Kind: string(kind), Model: model, BaseUrl: base,
			Ciphertext: sealed.Ciphertext, WrappedKey: sealed.WrappedKey, KeyID: keyID, MonthlyTokenCap: monthlyCap, CreatedBy: &uid,
		})
	}
	if err != nil {
		if store.IsUniqueViolation(err) {
			return nil, errConflict("another provider with this name was just created; try again")
		}
		return nil, err
	}
	h.audit(ctx, "ai.provider.put", name, map[string]any{"kind": kind, "model": model, "baseURL": base, "keyChanged": b.ApiKey != nil, "monthlyTokenCap": monthlyCap})
	row, err := h.st.GetAIProvider(ctx, db.GetAIProviderParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		return nil, err
	}
	used, err := h.aiTokensThisMonth(ctx, p.OrgID)
	if err != nil {
		return nil, err
	}
	if exists {
		return gen.PutAIProvider200JSONResponse(aiProviderOf(row, used)), nil
	}
	return gen.PutAIProvider201JSONResponse(aiProviderOf(row, used)), nil
}

func (h *handlers) DeleteAIProvider(ctx context.Context, req gen.DeleteAIProviderRequestObject) (gen.DeleteAIProviderResponseObject, error) {
	p, err := need(ctx, auth.PermManageUsers)
	if err != nil {
		return nil, err
	}
	row, err := h.st.GetAIProvider(ctx, db.GetAIProviderParams{ID: req.ProviderId, OrgID: p.OrgID})
	if err != nil {
		return nil, notFoundOr(err, "AI provider")
	}
	if _, err := h.st.DeleteAIProvider(ctx, db.DeleteAIProviderParams{ID: row.ID, OrgID: p.OrgID}); err != nil {
		return nil, err
	}
	h.audit(ctx, "ai.provider.delete", row.Name, nil)
	return gen.DeleteAIProvider204Response{}, nil
}

// pickProvider resolves the provider for a job: the requested one, the
// only one, or the one named "default".
func (h *handlers) pickProvider(ctx context.Context, org uuid.UUID, id *uuid.UUID) (db.AiProvider, error) {
	if id != nil {
		row, err := h.st.GetAIProvider(ctx, db.GetAIProviderParams{ID: *id, OrgID: org})
		if err != nil {
			if store.IsNotFound(err) {
				return row, errInvalid("AI provider not found")
			}
			return row, err
		}
		return row, nil
	}
	rows, err := h.st.ListAIProviders(ctx, org)
	if err != nil {
		return db.AiProvider{}, err
	}
	if len(rows) == 1 {
		return rows[0], nil
	}
	for _, r := range rows {
		if r.Name == "default" {
			return r, nil
		}
	}
	if len(rows) == 0 {
		return db.AiProvider{}, errConflict("no AI provider is configured; an admin can add one with POST /ai/providers")
	}
	return db.AiProvider{}, errInvalid("several AI providers are configured; choose one with providerId")
}

func (h *handlers) providerClient(row db.AiProvider) (provider.Provider, error) {
	key := ""
	if len(row.Ciphertext) > 0 {
		kr, err := h.keyring()
		if err != nil {
			return nil, err
		}
		sealed := keyring.Sealed{Ciphertext: row.Ciphertext, WrappedKey: row.WrappedKey}
		if row.KeyID != nil {
			sealed.KeyID = *row.KeyID
		}
		pt, err := kr.Open(sealed, aiProviderAAD(row.OrgID, row.ID))
		if err != nil {
			return nil, fmt.Errorf("AI provider %s: %w", row.Name, err)
		}
		key = string(pt)
	}
	p, err := h.cfg.AI.NewProvider(provider.Config{Kind: provider.Kind(row.Kind), Model: row.Model, BaseURL: row.BaseUrl, APIKey: key})
	if err != nil {
		return nil, errConflict("AI provider " + row.Name + ": " + err.Error())
	}
	return p, nil
}

// Jobs.

func (h *handlers) CreateAIJob(ctx context.Context, req gen.CreateAIJobRequestObject) (gen.CreateAIJobResponseObject, error) {
	p, pr, err := h.project(ctx, req.ProjectId, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	b := req.Body
	str := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	in := ai.Inputs{
		Description: strings.TrimSpace(str(b.Description)),
		OpenAPI:     []byte(str(b.Openapi)), HAR: []byte(str(b.Har)), AccessLog: []byte(str(b.AccessLog)),
	}
	switch {
	case in.Description == "" && len(in.OpenAPI) == 0 && len(in.HAR) == 0 && len(in.AccessLog) == 0:
		return nil, errInvalid("give at least one input: description, openapi, har or accessLog")
	case len(in.Description) > maxAIDescription:
		return nil, errInvalid("description is longer than 20,000 characters")
	case len(in.OpenAPI) > maxAISpec:
		return nil, errInvalid("openapi is larger than 5 MiB")
	case len(in.HAR) > maxAITraffic || len(in.AccessLog) > maxAITraffic:
		return nil, errInvalid("har and accessLog are limited to 20 MiB each")
	}
	dryRun := b.DryRun == nil || *b.DryRun
	maxRepairs := ai.DefaultMaxRepairs
	if b.MaxRepairs != nil {
		if *b.MaxRepairs < 0 || *b.MaxRepairs > 3 {
			return nil, errInvalid("maxRepairs must be between 0 and 3")
		}
		maxRepairs = *b.MaxRepairs
	}
	if maxRepairs == 0 {
		maxRepairs = -1 // the pipeline reads 0 as the default
	}

	opts := ai.Options{DryRun: dryRun, MaxRepairs: maxRepairs, OmitBaseURL: true, DataDir: h.cfg.DataDir, ConfineData: true, Transport: h.cfg.AI.Transport}
	var targetID *uuid.UUID
	if b.TargetId != nil {
		tg, err := h.st.GetTarget(ctx, db.GetTargetParams{ID: *b.TargetId, OrgID: p.OrgID})
		if err != nil || tg.ProjectID != pr.ID {
			return nil, errInvalid("target not found in this project")
		}
		opts.Target, opts.AllowHosts = tg.BaseUrl, tg.AllowHosts
		targetID = &tg.ID
	} else if dryRun {
		return nil, errInvalid("targetId is required for the dry run (or set dryRun to false)")
	}
	var scenarioID *uuid.UUID
	if b.ScenarioId != nil {
		sc, err := h.st.GetScenario(ctx, db.GetScenarioParams{ID: *b.ScenarioId, OrgID: p.OrgID})
		if err != nil || sc.ProjectID != pr.ID {
			return nil, errInvalid("scenario not found in this project")
		}
		v, err := h.st.GetLatestScenarioVersion(ctx, sc.ID)
		if err != nil {
			return nil, err
		}
		in.Existing = []byte(v.Yaml)
		scenarioID = &sc.ID
	}

	prow, err := h.pickProvider(ctx, p.OrgID, b.ProviderId)
	if err != nil {
		return nil, err
	}
	used, err := h.aiTokensThisMonth(ctx, p.OrgID)
	if err != nil {
		return nil, err
	}
	if used >= prow.MonthlyTokenCap {
		return nil, &apiError{status: 429, code: "ai_token_cap", msg: fmt.Sprintf("the organisation used %d AI tokens this month, which reaches the cap of %d for provider %s", used, prow.MonthlyTokenCap, prow.Name)}
	}
	if h.ai.queued.Load() >= int64(h.cfg.AI.MaxQueued) {
		return nil, &apiError{status: 429, code: "ai_queue_full", msg: "too many AI generation jobs are waiting; try again shortly"}
	}
	client, err := h.providerClient(prow)
	if err != nil {
		return nil, err
	}
	opts.Provider = client
	opts.TokenBudget = prow.MonthlyTokenCap - used
	if dryRun {
		if opts.Secrets, err = h.projectSecrets(ctx, pr.ID); err != nil {
			return nil, err
		}
	}

	// Record what was given, never the contents.
	sizes := map[string]int{}
	for k, v := range map[string]int{"description": len(in.Description), "openapi": len(in.OpenAPI), "har": len(in.HAR), "accessLog": len(in.AccessLog)} {
		if v > 0 {
			sizes[k] = v
		}
	}
	inputsJSON, _ := json.Marshal(sizes)
	id, uid := uuid.New(), p.UserID
	provID := prow.ID
	err = h.st.CreateAIJob(ctx, db.CreateAIJobParams{
		ID: id, OrgID: p.OrgID, ProjectID: pr.ID, ProviderID: &provID, ProviderKind: prow.Kind, Model: client.Model(),
		TargetID: targetID, ScenarioID: scenarioID, DryRun: dryRun, Inputs: inputsJSON, CreatedBy: &uid,
	})
	if err != nil {
		return nil, err
	}
	if err := h.st.SetAIJobOwner(ctx, db.SetAIJobOwnerParams{ID: id, OwnerReplica: &h.replica.id}); err != nil {
		return nil, err
	}
	h.audit(ctx, "ai.job.create", pr.Name, map[string]any{"job": id, "provider": prow.Name, "model": client.Model(), "inputs": sizes, "dryRun": dryRun, "target": opts.Target})
	h.ai.launch(aiJobSpec{id: id, inputs: in, opts: opts})

	row, err := h.st.GetAIJob(ctx, db.GetAIJobParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		return nil, err
	}
	return gen.CreateAIJob202JSONResponse(aiJobOf(row, h.creators(ctx, p.OrgID))), nil
}

func aiJobSummaryOf(r db.ListAIJobsRow, creators map[uuid.UUID]string) gen.AIJobSummary {
	out := gen.AIJobSummary{
		Id: r.ID, ProjectId: r.ProjectID, Status: gen.AIJobStatus(r.Status), Stage: r.Stage, ProviderKind: r.ProviderKind,
		Model: r.Model, Usage: gen.AIUsage{InputTokens: r.InputTokens, OutputTokens: r.OutputTokens}, DryRun: r.DryRun,
		CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt, ApprovedAt: r.ApprovedAt,
	}
	if r.Error != "" {
		e := r.Error
		out.Error = &e
	}
	if r.CreatedBy != nil {
		if n, ok := creators[*r.CreatedBy]; ok {
			out.CreatedBy = &n
		}
	}
	return out
}

func aiJobOf(r db.AiJob, creators map[uuid.UUID]string) gen.AIJob {
	s := aiJobSummaryOf(db.ListAIJobsRow{
		ID: r.ID, ProjectID: r.ProjectID, Status: r.Status, Stage: r.Stage, ProviderKind: r.ProviderKind, Model: r.Model,
		InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, DryRun: r.DryRun, Error: r.Error,
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt, ApprovedAt: r.ApprovedAt,
	}, creators)
	out := gen.AIJob{
		Id: s.Id, ProjectId: s.ProjectId, Status: s.Status, Stage: s.Stage, ProviderKind: s.ProviderKind, Model: s.Model,
		Usage: s.Usage, DryRun: s.DryRun, Error: s.Error, CreatedBy: s.CreatedBy, CreatedAt: s.CreatedAt,
		FinishedAt: s.FinishedAt, ApprovedAt: s.ApprovedAt,
		Round: int(r.Round), TargetId: r.TargetID, ScenarioId: r.ScenarioID, StartedAt: r.StartedAt,
		ApprovedScenarioId: r.ApprovedScenarioID, Journeys: []gen.AIJourney{}, Problems: []gen.AIProblem{},
	}
	if r.ApprovedVersion != nil {
		v := int(*r.ApprovedVersion)
		out.ApprovedVersion = &v
	}
	if r.Yaml != "" {
		y := r.Yaml
		out.Yaml = &y
	}
	if r.Diff != "" {
		d := r.Diff
		out.Diff = &d
	}
	var res struct {
		Journeys []gen.AIJourney `json:"journeys"`
		Problems []gen.AIProblem `json:"problems"`
	}
	if json.Unmarshal(r.Result, &res) == nil {
		if res.Journeys != nil {
			out.Journeys = res.Journeys
		}
		if res.Problems != nil {
			out.Problems = res.Problems
		}
	}
	return out
}

func (h *handlers) ListAIJobs(ctx context.Context, req gen.ListAIJobsRequestObject) (gen.ListAIJobsResponseObject, error) {
	p, pr, err := h.project(ctx, req.ProjectId, auth.PermView)
	if err != nil {
		return nil, err
	}
	limit := int32(50)
	if req.Params.Limit != nil {
		limit = int32(min(max(*req.Params.Limit, 1), 200)) //nolint:gosec // bounded
	}
	rows, err := h.st.ListAIJobs(ctx, db.ListAIJobsParams{ProjectID: pr.ID, Limit: limit})
	if err != nil {
		return nil, err
	}
	cr := h.creators(ctx, p.OrgID)
	out := gen.ListAIJobs200JSONResponse{}
	for _, r := range rows {
		out = append(out, aiJobSummaryOf(r, cr))
	}
	return out, nil
}

func (h *handlers) GetAIJob(ctx context.Context, req gen.GetAIJobRequestObject) (gen.GetAIJobResponseObject, error) {
	p, err := need(ctx, auth.PermView)
	if err != nil {
		return nil, err
	}
	row, err := h.st.GetAIJob(ctx, db.GetAIJobParams{ID: req.JobId, OrgID: p.OrgID})
	if err != nil {
		return nil, notFoundOr(err, "AI job")
	}
	return gen.GetAIJob200JSONResponse(aiJobOf(row, h.creators(ctx, p.OrgID))), nil
}

func (h *handlers) ApproveAIJob(ctx context.Context, req gen.ApproveAIJobRequestObject) (gen.ApproveAIJobResponseObject, error) {
	p, err := need(ctx, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	job, err := h.st.GetAIJob(ctx, db.GetAIJobParams{ID: req.JobId, OrgID: p.OrgID})
	if err != nil {
		return nil, notFoundOr(err, "AI job")
	}
	b := req.Body
	allow := b.AllowUnvalidated != nil && *b.AllowUnvalidated
	switch {
	case job.ApprovedAt != nil:
		return nil, errConflict("this job was already approved")
	case job.Status == aiQueued || job.Status == aiRunning:
		return nil, errConflict("the job has not finished yet")
	case job.Status == aiFailed || job.Yaml == "":
		return nil, errConflict("the job failed and has no usable proposal")
	case job.Status == aiNeedsReview && !allow:
		var flagged []string
		for _, j := range aiJobOf(job, nil).Journeys {
			if j.Status == gen.AIJourneyFlagged {
				flagged = append(flagged, j.Name)
			}
		}
		return nil, errConflict("some journeys did not pass their dry run (" + strings.Join(flagged, ", ") + "); review them and approve with allowUnvalidated")
	}

	target := b.ScenarioId
	if target == nil {
		target = job.ScenarioID
	}
	if target != nil {
		sc, err := h.st.GetScenario(ctx, db.GetScenarioParams{ID: *target, OrgID: p.OrgID})
		if err != nil || sc.ProjectID != job.ProjectID {
			return nil, errInvalid("scenario not found in the job's project")
		}
	}
	if n, err := h.st.ClaimAIJobApproval(ctx, job.ID); err != nil {
		return nil, err
	} else if n == 0 {
		return nil, errConflict("this job was already approved")
	}
	release := func() { _ = h.st.ReleaseAIJobApproval(context.WithoutCancel(ctx), job.ID) }

	msg := fmt.Sprintf("generated by AI job %s (%s/%s)", job.ID, job.ProviderKind, job.Model)
	if b.Message != nil && strings.TrimSpace(*b.Message) != "" {
		msg = strings.TrimSpace(*b.Message)
	}
	body := &gen.ScenarioVersionCreate{Yaml: job.Yaml, Message: &msg}
	var sc gen.Scenario
	version := 1
	if target != nil {
		resp, err := h.CreateScenarioVersion(ctx, gen.CreateScenarioVersionRequestObject{ScenarioId: *target, Body: body})
		if err != nil {
			release()
			return nil, err
		}
		v, ok := resp.(gen.CreateScenarioVersion201JSONResponse)
		if !ok {
			release()
			return nil, errors.New("unexpected response saving the scenario version")
		}
		version = v.Version
		row, err := h.st.GetScenario(ctx, db.GetScenarioParams{ID: *target, OrgID: p.OrgID})
		if err != nil {
			return nil, err
		}
		if sc, err = h.scenarioOut(ctx, p.OrgID, row); err != nil {
			return nil, err
		}
	} else {
		resp, err := h.CreateScenario(ctx, gen.CreateScenarioRequestObject{ProjectId: job.ProjectID, Body: body})
		if err != nil {
			release()
			return nil, err
		}
		c, ok := resp.(gen.CreateScenario201JSONResponse)
		if !ok {
			release()
			return nil, errors.New("unexpected response creating the scenario")
		}
		sc = gen.Scenario(c)
	}
	v32 := int32(version) //nolint:gosec // versions are small
	if err := h.st.SetAIJobApproved(ctx, db.SetAIJobApprovedParams{ID: job.ID, ApprovedScenarioID: &sc.Id, ApprovedVersion: &v32}); err != nil {
		return nil, err
	}
	h.audit(ctx, "ai.job.approve", sc.Name, map[string]any{"job": job.ID, "version": version, "status": job.Status, "allowUnvalidated": allow})
	return gen.ApproveAIJob201JSONResponse{Scenario: sc, Version: version}, nil
}
