package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/observe"
	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/runner"
	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/store/db"
)

func runOf(r db.GetRunRow) gen.Run {
	out := gen.Run{
		Id: r.ID, ProjectId: r.ProjectID, ScenarioId: r.ScenarioID, ScenarioName: &r.ScenarioName,
		ScenarioVersion: int(r.ScenarioVersion), TargetId: r.TargetID, TargetURL: &r.TargetUrl,
		Status: gen.RunStatus(r.Status), StopReason: r.StopReason, Error: r.Error,
		CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, EndedAt: r.EndedAt, Workers: ptr(int(r.Workers)),
	}
	if r.Verdict != nil {
		v := gen.RunVerdict(*r.Verdict)
		out.Verdict = &v
	}
	if r.Note != "" {
		out.Note = &r.Note
	}
	var ov gen.RunOverrides
	if json.Unmarshal(r.Overrides, &ov) == nil {
		out.Overrides = &ov
	}
	if len(r.Plan) > 0 {
		var ps gen.PlanSummary
		if json.Unmarshal(r.Plan, &ps) == nil {
			out.Plan = &ps
		}
	}
	if len(r.Summary) > 0 {
		var sm gen.RunSummary
		if json.Unmarshal(r.Summary, &sm) == nil {
			out.Summary = &sm
		}
	}
	return out
}

func listRowToRun(r db.ListRunsRow) gen.Run {
	return runOf(db.GetRunRow(r))
}

func (h *handlers) CreateRun(ctx context.Context, req gen.CreateRunRequestObject) (gen.CreateRunResponseObject, error) {
	p, pr, err := h.project(ctx, req.ProjectId, auth.PermRun)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := runInput{scenarioID: b.ScenarioId, targetID: b.TargetId, version: b.Version, overrides: b.Overrides}
	if b.Env != nil {
		in.env = *b.Env
	}
	if b.Workers != nil {
		in.workers = *b.Workers
	}
	if b.Note != nil {
		in.note = *b.Note
	}
	prep, err := h.prepareRun(ctx, p.OrgID, pr, in)
	if err != nil {
		return nil, err
	}
	row, err := h.startRun(ctx, p, pr, prep)
	if err != nil {
		return nil, err
	}
	return gen.CreateRun201JSONResponse(runOf(row)), nil
}

// runInput is what a run is started from: a request to POST /runs or a
// schedule.
type runInput struct {
	scenarioID uuid.UUID
	targetID   uuid.UUID
	version    *int // nil for the latest
	overrides  *gen.RunOverrides
	env        map[string]string
	workers    int
	note       string
}

// preparedRun is a validated run, ready to record and launch.
type preparedRun struct {
	in      runInput
	sc      db.Scenario
	tg      db.Target
	version int32
	s       *scenario.Scenario
	yaml    []byte
	prog    *scenario.Program
	plan    *scenario.Plan
	ov      gen.RunOverrides
	obs     *observe.Config
	faults  *runner.FaultPlan
	secrets map[string]string
}

// prepareRun loads and checks everything a run needs (scenario, target,
// overrides, caps, integrations and secrets) without starting it. Both
// POST /runs and schedules go through it, so they accept the same runs.
func (h *handlers) prepareRun(ctx context.Context, org uuid.UUID, pr db.Project, in runInput) (*preparedRun, error) {
	sc, err := h.st.GetScenario(ctx, db.GetScenarioParams{ID: in.scenarioID, OrgID: org})
	if err != nil || sc.ProjectID != pr.ID {
		return nil, errInvalid("scenario not found in this project")
	}
	tg, err := h.st.GetTarget(ctx, db.GetTargetParams{ID: in.targetID, OrgID: org})
	if err != nil || tg.ProjectID != pr.ID {
		return nil, errInvalid("target not found in this project")
	}
	var ver db.ScenarioVersion
	if in.version != nil {
		ver, err = h.st.GetScenarioVersion(ctx, db.GetScenarioVersionParams{ScenarioID: sc.ID, Version: int32(*in.version)}) //nolint:gosec // small
	} else {
		ver, err = h.st.GetLatestScenarioVersion(ctx, sc.ID)
	}
	if err != nil {
		return nil, notFoundOr(err, "scenario version")
	}

	s, err := scenario.Decode([]byte(ver.Yaml))
	if err != nil {
		return nil, errInvalid(err.Error())
	}
	var ov gen.RunOverrides
	if in.overrides != nil {
		ov = *in.overrides
		if err := applyOverrides(s, ov); err != nil {
			return nil, err
		}
	}
	if err := confineDataFiles(s, h.cfg.DataDir); err != nil {
		return nil, err
	}
	// The run's target always wins over the file's base URL.
	s.Target.BaseURL = tg.BaseUrl
	if err := s.Validate(); err != nil {
		var ve *scenario.ValidationError
		if errors.As(err, &ve) {
			return nil, errInvalid("the scenario has problems", ve.Problems...)
		}
		return nil, errInvalid(err.Error())
	}
	prog, err := scenario.Compile(s)
	if err != nil {
		return nil, errInvalid(err.Error())
	}
	plan, err := s.Load.Plan()
	if err != nil {
		return nil, errInvalid(err.Error())
	}
	if plan.StopOnFail && len(prog.Thresholds) == 0 {
		return nil, errInvalid("the breakpoint shape needs at least one target, such as \"http.p95 < 500ms\"")
	}
	if err := h.checkCaps(tg, plan); err != nil {
		return nil, err
	}
	obs, err := h.observeFor(ctx, org, s.Observe)
	if err != nil {
		return nil, err
	}
	// Workers never need the observe block: the server queries and links
	// after the run, so it is not sent to them.
	s.Observe = nil
	faults, err := h.faultsFor(ctx, org, s.Faults)
	if err != nil {
		return nil, err
	}
	// Faults are injected by the server through the agent, never by workers.
	s.Faults = nil

	secrets, err := h.projectSecrets(ctx, pr.ID)
	if err != nil {
		return nil, err
	}
	if in.env == nil {
		in.env = map[string]string{}
	}
	yamlOut, err := s.Marshal()
	if err != nil {
		return nil, err
	}
	return &preparedRun{
		in: in, sc: sc, tg: tg, version: ver.Version, s: s, yaml: yamlOut, prog: prog, plan: plan,
		ov: ov, obs: obs, faults: faults, secrets: secrets,
	}, nil
}

func applyOverrides(s *scenario.Scenario, ov gen.RunOverrides) error {
	o := scenario.Overrides{}
	if ov.Shape != nil {
		o.Shape = *ov.Shape
	}
	if ov.Mode != nil {
		o.Mode = string(*ov.Mode)
	}
	if ov.Vus != nil {
		o.VUs = *ov.Vus
	}
	if ov.Rate != nil {
		o.Rate = *ov.Rate
	}
	if ov.Duration != nil {
		o.Duration = *ov.Duration
	}
	if ov.Start != nil {
		o.Start = *ov.Start
	}
	if ov.Max != nil {
		o.Max = *ov.Max
	}
	if err := o.Apply(s); err != nil {
		return errInvalid("overrides: " + err.Error())
	}
	return nil
}

// startRun records a prepared run as started by p, audits it and launches
// it in the background.
func (h *handlers) startRun(ctx context.Context, p *auth.Principal, pr db.Project, r *preparedRun) (db.GetRunRow, error) {
	ovJSON, _ := json.Marshal(r.ov)
	planJSON, _ := json.Marshal(planSummary(r.s))
	envJSON, _ := json.Marshal(redactedEnv(r.in.env))
	id, uid := uuid.New(), p.UserID
	err := h.st.CreateRun(ctx, db.CreateRunParams{
		ID: id, ProjectID: pr.ID, ScenarioID: r.sc.ID, ScenarioVersion: r.version, TargetID: r.tg.ID,
		Overrides: ovJSON, Plan: planJSON, Env: envJSON, Workers: int32(r.in.workers), Note: r.in.note, CreatedBy: &uid, //nolint:gosec // small
	})
	if err != nil {
		return db.GetRunRow{}, err
	}
	h.audit(ctx, "run.start", r.sc.Name+" → "+r.tg.BaseUrl, map[string]any{
		"run": id, "version": r.version, "peak": r.plan.Peak(), "mode": r.plan.Mode, "duration": r.plan.TotalDuration().String(),
	})

	h.runs.launch(&activeRun{id: id, org: p.OrgID, project: pr.ID, target: r.tg.ID, scenario: r.sc.ID, obs: r.obs, faults: r.faults, link: trace.LinkFromContext(ctx)},
		ExecSpec{
			RunID: id.String(), Scenario: r.s, YAML: r.yaml, Env: r.in.env, Secrets: r.secrets,
			AllowHosts: r.tg.AllowHosts, TargetHost: r.tg.Host, Workers: r.in.workers,
		}, r.prog, r.plan)

	return h.st.GetRun(ctx, db.GetRunParams{ID: id, OrgID: p.OrgID})
}

// redactedEnv keeps env names for the record but not values, which may be
// sensitive even though they are not declared as secrets.
func redactedEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for k := range env {
		out[k] = "…"
	}
	return out
}

// checkCaps applies, in order: the server's hard caps, the low caps for
// unverified public targets, and the target's own caps.
func (h *handlers) checkCaps(tg db.Target, plan *scenario.Plan) error {
	if err := safety.CheckPlan(plan, h.cfg.HardCaps); err != nil {
		return errForbidden("over the server's limits: " + err.Error())
	}
	if !tg.Private && tg.VerifiedAt == nil {
		if err := safety.CheckPlan(plan, safety.UnverifiedPublicCaps); err != nil {
			return errForbidden(fmt.Sprintf("%s is public and its ownership is not verified, so load is capped: %v. Verify the target to lift this", tg.Host, err))
		}
	}
	c := safety.Caps{}
	if tg.MaxRate != nil {
		c.MaxRate = *tg.MaxRate
	}
	if tg.MaxVus != nil {
		c.MaxVUs = int(*tg.MaxVus)
	}
	if tg.MaxDurationS != nil {
		c.MaxDuration = time.Duration(*tg.MaxDurationS) * time.Second
	}
	if err := safety.CheckPlan(plan, c); err != nil {
		return errForbidden("over this target's caps: " + err.Error())
	}
	return nil
}

func (h *handlers) run(ctx context.Context, id uuid.UUID, min auth.Role) (db.GetRunRow, error) {
	p, err := need(ctx, min)
	if err != nil {
		return db.GetRunRow{}, err
	}
	r, err := h.st.GetRun(ctx, db.GetRunParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		return db.GetRunRow{}, notFoundOr(err, "run")
	}
	return r, nil
}

func (h *handlers) ListRuns(ctx context.Context, req gen.ListRunsRequestObject) (gen.ListRunsResponseObject, error) {
	_, pr, err := h.project(ctx, req.ProjectId, auth.PermView)
	if err != nil {
		return nil, err
	}
	limit, before := 50, time.Now().Add(time.Hour)
	if req.Params.Limit != nil {
		limit = min(max(*req.Params.Limit, 1), 200)
	}
	if req.Params.Before != nil {
		before = *req.Params.Before
	}
	rows, err := h.st.ListRuns(ctx, db.ListRunsParams{ProjectID: pr.ID, Limit: int32(limit), Before: before, ScenarioID: req.Params.ScenarioId}) //nolint:gosec // bounded
	if err != nil {
		return nil, err
	}
	out := gen.ListRuns200JSONResponse{}
	for _, r := range rows {
		out = append(out, listRowToRun(r))
	}
	return out, nil
}

func (h *handlers) GetRun(ctx context.Context, req gen.GetRunRequestObject) (gen.GetRunResponseObject, error) {
	r, err := h.run(ctx, req.RunId, auth.PermView)
	if err != nil {
		return nil, err
	}
	return gen.GetRun200JSONResponse(runOf(r)), nil
}

func (h *handlers) StopRun(ctx context.Context, req gen.StopRunRequestObject) (gen.StopRunResponseObject, error) {
	r, err := h.run(ctx, req.RunId, auth.PermRun)
	if err != nil {
		return nil, err
	}
	if err := h.controlRun(ctx, r, "stop", auth.FromContext(ctx).Actor()); err != nil {
		if errors.Is(err, errRunNotActive) {
			return nil, errConflict("the run is not active")
		}
		return nil, err
	}
	h.audit(ctx, "run.stop", r.ID.String(), nil)
	return gen.StopRun202Response{}, nil
}

func (h *handlers) KillRun(ctx context.Context, req gen.KillRunRequestObject) (gen.KillRunResponseObject, error) {
	r, err := h.run(ctx, req.RunId, auth.PermRun)
	if err != nil {
		return nil, err
	}
	if err := h.controlRun(ctx, r, "kill", auth.FromContext(ctx).Actor()); err != nil && !errors.Is(err, errRunNotActive) {
		return nil, err
	}
	h.audit(ctx, "run.kill", r.ID.String(), nil)
	return gen.KillRun202Response{}, nil
}

func (h *handlers) KillAllRuns(ctx context.Context, _ gen.KillAllRunsRequestObject) (gen.KillAllRunsResponseObject, error) {
	p, err := need(ctx, auth.PermRun)
	if err != nil {
		return nil, err
	}
	ids, err := h.st.ListActiveRuns(ctx, p.OrgID)
	if err != nil {
		return nil, err
	}
	// Runs here die now; the other replicas kill theirs when the message
	// arrives. Every active run of the organisation is reported.
	h.runs.killOrg(p.OrgID, p.Actor())
	if len(ids) > 0 {
		if err := h.publishControl(ctx, controlMsg{Op: "kill_all", Org: p.OrgID, By: p.Actor()}); err != nil {
			h.log.Error("could not tell other replicas to kill their runs", "error", err)
		}
	}
	killed := append([]uuid.UUID{}, ids...)
	h.audit(ctx, "run.kill_all", "", map[string]any{"runs": killed})
	return gen.KillAllRuns200JSONResponse{Killed: killed}, nil
}

func (h *handlers) StreamRun(context.Context, gen.StreamRunRequestObject) (gen.StreamRunResponseObject, error) {
	// Served by Server.streamRun, which is mounted ahead of the generated router.
	return nil, errors.New("unreachable")
}

func (h *handlers) GetRunTimeline(ctx context.Context, req gen.GetRunTimelineRequestObject) (gen.GetRunTimelineResponseObject, error) {
	r, err := h.run(ctx, req.RunId, auth.PermView)
	if err != nil {
		return nil, err
	}
	rows, err := h.st.ListRunPoints(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	out := gen.GetRunTimeline200JSONResponse{}
	for _, row := range rows {
		out = append(out, pointRow(row))
	}
	return out, nil
}

func (h *handlers) GetRunReport(ctx context.Context, req gen.GetRunReportRequestObject) (gen.GetRunReportResponseObject, error) {
	r, err := h.run(ctx, req.RunId, auth.PermView)
	if err != nil {
		return nil, err
	}
	raw, err := h.st.GetReport(ctx, r.ID)
	if err != nil {
		if r.Status == statusFailed {
			return nil, errConflict("the run failed before producing a report")
		}
		return nil, notFoundOr(err, "report (the run has not finished)")
	}
	rep, err := report.ReadJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	format := gen.Json
	if req.Params.Format != nil {
		format = *req.Params.Format
	}
	var buf bytes.Buffer
	name := "stampede-" + rep.Scenario + "-" + r.ID.String()[:8]
	setDownload := func(ext string) {
		if w, _ := httpFrom(ctx); w != nil {
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`, url.PathEscape(name), ext))
		}
	}
	switch format {
	case gen.Html:
		if err := rep.WriteHTML(&buf); err != nil {
			return nil, err
		}
		setDownload("html")
		return gen.GetRunReport200TexthtmlResponse{Body: &buf, ContentLength: int64(buf.Len())}, nil
	case gen.Junit:
		if err := rep.WriteJUnit(&buf); err != nil {
			return nil, err
		}
		setDownload("xml")
		return gen.GetRunReport200ApplicationxmlResponse{Body: &buf, ContentLength: int64(buf.Len())}, nil
	case gen.Markdown:
		rep.WriteMarkdown(&buf)
		setDownload("md")
		return gen.GetRunReport200TextmarkdownResponse{Body: &buf, ContentLength: int64(buf.Len())}, nil
	case gen.Csv, gen.TimelineCsv:
		write, suffix := rep.WriteCSV, ""
		if format == gen.TimelineCsv {
			write, suffix = rep.WriteTimelineCSV, "-timeline"
		}
		if err := write(&buf); err != nil {
			return nil, err
		}
		name += suffix
		setDownload("csv")
		return gen.GetRunReport200TextcsvResponse{Body: &buf, ContentLength: int64(buf.Len())}, nil
	default:
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		return gen.GetRunReport200JSONResponse(m), nil
	}
}

func (h *handlers) ListWorkers(ctx context.Context, _ gen.ListWorkersRequestObject) (gen.ListWorkersResponseObject, error) {
	if _, err := need(ctx, auth.PermView); err != nil {
		return nil, err
	}
	out := gen.ListWorkers200JSONResponse{}
	if h.cfg.Workers == nil {
		return out, nil
	}
	for _, w := range h.cfg.Workers() {
		labels := w.Labels
		gw := gen.Worker{
			Id: w.ID, Name: w.Name, Region: w.Region, Version: &w.Version, Labels: &labels,
			Cpus: &w.CPUs, MemoryBytes: ptr(int(w.MemoryBytes)), Status: gen.WorkerStatus(strings.ToLower(w.Status)),
			ConnectedAt: w.ConnectedAt, LastSeenAt: w.LastSeenAt,
		}
		if w.Protocols != nil {
			gw.Protocols = &w.Protocols
		}
		if w.Plugins != nil {
			gw.Plugins = &w.Plugins
		}
		if w.RunID != "" {
			gw.RunId = &w.RunID
		}
		out = append(out, gw)
	}
	return out, nil
}

// confineDataFiles stops a scenario run by the server from reading
// arbitrary server files through CSV or JSON feeders (and sending their
// contents to the target). File feeders must name files inside the
// configured data directory; paths are rewritten to absolute ones there.
// Workers must have the same directory at the same path.
func confineDataFiles(s *scenario.Scenario, dataDir string) error {
	for name, f := range s.Data {
		for _, p := range []*string{&f.CSV, &f.JSON} {
			if *p == "" {
				continue
			}
			if dataDir == "" {
				return errInvalid(fmt.Sprintf("data.%s reads a file, which needs the server to be started with --data-dir; use list or range feeders instead", name))
			}
			clean := filepath.Clean("/" + *p)
			full := filepath.Join(dataDir, clean)
			rel, err := filepath.Rel(dataDir, full)
			if err != nil || strings.HasPrefix(rel, "..") {
				return errInvalid(fmt.Sprintf("data.%s: %q is outside the data directory", name, *p))
			}
			*p = full
		}
		s.Data[name] = f
	}
	if r := s.Load.Replay; r != nil && r.File != "" {
		if dataDir == "" {
			return errInvalid("load.replay reads a file, which needs the server to be started with --data-dir")
		}
		full := filepath.Join(dataDir, filepath.Clean("/"+r.File))
		if rel, err := filepath.Rel(dataDir, full); err != nil || strings.HasPrefix(rel, "..") {
			return errInvalid(fmt.Sprintf("load.replay.file: %q is outside the data directory", r.File))
		}
		r.File = full
		if err := s.LoadReplay(); err != nil {
			return errInvalid(err.Error())
		}
	}
	return nil
}
