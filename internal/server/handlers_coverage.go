package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/ai"
	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/store/db"
)

// specFetchTimeout bounds fetching an OpenAPI document by URL.
const specFetchTimeout = 15 * time.Second

// scenarioAt loads a version of a scenario (default the latest) and
// decodes it.
func (h *handlers) scenarioAt(ctx context.Context, sc db.Scenario, version *int) (*scenario.Scenario, int, error) {
	var ver db.ScenarioVersion
	var err error
	if version != nil {
		ver, err = h.st.GetScenarioVersion(ctx, db.GetScenarioVersionParams{ScenarioID: sc.ID, Version: int32(*version)}) //nolint:gosec // small
	} else {
		ver, err = h.st.GetLatestScenarioVersion(ctx, sc.ID)
	}
	if err != nil {
		return nil, 0, notFoundOr(err, "scenario version")
	}
	s, err := scenario.Decode([]byte(ver.Yaml))
	if err != nil {
		return nil, 0, errInvalid(err.Error())
	}
	return s, int(ver.Version), nil
}

// apiSpec returns an OpenAPI document given inline or by URL. A URL must
// point at the host of one of the project's targets, or a host a target
// allows, so the server never fetches from anywhere else on request.
func (h *handlers) apiSpec(ctx context.Context, project uuid.UUID, inline, specURL *string, what string) ([]byte, error) {
	if inline != nil && strings.TrimSpace(*inline) != "" {
		if len(*inline) > maxAISpec {
			return nil, errInvalid(what + " is larger than 5 MiB")
		}
		return []byte(*inline), nil
	}
	if specURL == nil || strings.TrimSpace(*specURL) == "" {
		return nil, nil
	}
	// Whoever may add targets may have the server fetch from them.
	if p := auth.FromContext(ctx); p == nil || !p.Can(auth.PermEditScenarios) {
		return nil, errForbidden(fmt.Sprintf("fetching %s by URL needs the %s role or higher; paste the document instead", what, auth.PermEditScenarios))
	}
	u, err := url.Parse(strings.TrimSpace(*specURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errInvalid(what + " URL must be an absolute http(s) URL")
	}
	targets, err := h.st.ListTargets(ctx, project)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, t := range targets {
		if tu, err := url.Parse(t.BaseUrl); err == nil {
			allowed[strings.ToLower(tu.Hostname())] = true
		}
		for _, a := range t.AllowHosts {
			allowed[strings.ToLower(a)] = true
		}
	}
	if !allowed[strings.ToLower(u.Hostname())] {
		return nil, errInvalid(fmt.Sprintf("%s URL: %s is not the host of a target in this project; add the target first or paste the document", what, u.Hostname()))
	}
	client := &http.Client{
		Timeout: specFetchTimeout,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 5 || !allowed[strings.ToLower(r.URL.Hostname())] {
				return errors.New("redirect to a host that is not a target")
			}
			return nil
		},
	}
	if t := h.cfg.AI.Transport; t != nil {
		client.Transport = t
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errInvalid(err.Error())
	}
	req.Header.Set("Accept", "application/json, application/yaml, text/yaml, */*")
	res, err := client.Do(req)
	if err != nil {
		return nil, errInvalid(fmt.Sprintf("fetch %s: %v", what, err))
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errInvalid(fmt.Sprintf("fetch %s: %s answered %s", what, u.Host, res.Status))
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxAISpec+1))
	if err != nil {
		return nil, errInvalid(fmt.Sprintf("fetch %s: %v", what, err))
	}
	if len(b) > maxAISpec {
		return nil, errInvalid(what + " is larger than 5 MiB")
	}
	return b, nil
}

// understandSpec reads the endpoints of an OpenAPI document.
func understandSpec(spec []byte, what string) (*ai.Understanding, error) {
	u, err := ai.Understand(ai.Inputs{OpenAPI: spec}, ai.NewRedactor())
	if err != nil {
		return nil, errInvalid(fmt.Sprintf("%s: %v", what, err))
	}
	if len(u.Endpoints) == 0 {
		return nil, errInvalid(what + " has no endpoints")
	}
	return u, nil
}

func requestRefs(rs []ai.RequestRef) []gen.RequestRef {
	out := make([]gen.RequestRef, 0, len(rs))
	for _, r := range rs {
		out = append(out, gen.RequestRef{Journey: r.Journey, Method: r.Method, Url: r.URL})
	}
	return out
}

func driftEndpoints(es []ai.Endpoint) []gen.DriftEndpoint {
	out := make([]gen.DriftEndpoint, 0, len(es))
	for _, e := range es {
		out = append(out, gen.DriftEndpoint{Method: e.Method, Path: e.Path})
	}
	return out
}

// ScenarioCoverage maps a scenario's requests onto an API's endpoints,
// as stampede coverage does.
func (h *handlers) ScenarioCoverage(ctx context.Context, req gen.ScenarioCoverageRequestObject) (gen.ScenarioCoverageResponseObject, error) {
	_, sc, err := h.scenario(ctx, req.ScenarioId, auth.PermView)
	if err != nil {
		return nil, err
	}
	b := req.Body
	s, version, err := h.scenarioAt(ctx, sc, b.Version)
	if err != nil {
		return nil, err
	}
	spec, err := h.apiSpec(ctx, sc.ProjectID, b.Openapi, b.SpecURL, "openapi")
	if err != nil {
		return nil, err
	}
	if spec == nil {
		return nil, errInvalid("give the API as openapi or specURL")
	}
	u, err := understandSpec(spec, "openapi")
	if err != nil {
		return nil, err
	}
	c := ai.CoverageOf(s, u)
	out := gen.ScenarioCoverage{
		Version: version, Covered: c.Covered, Total: c.Total, Templated: c.Templated,
		Endpoints: make([]gen.CoverageEndpoint, 0, len(c.Endpoints)), Unmatched: requestRefs(c.Unmatched),
	}
	for _, e := range c.Endpoints {
		ce := gen.CoverageEndpoint{Method: e.Method, Path: e.Path, Journeys: append([]string{}, e.Journeys...)}
		if e.Summary != "" {
			ce.Summary = ptr(e.Summary)
		}
		out.Endpoints = append(out.Endpoints, ce)
	}
	return gen.ScenarioCoverage200JSONResponse(out), nil
}

// ScenarioDrift finds journeys an API change broke, as stampede drift
// does.
func (h *handlers) ScenarioDrift(ctx context.Context, req gen.ScenarioDriftRequestObject) (gen.ScenarioDriftResponseObject, error) {
	p, sc, err := h.scenario(ctx, req.ScenarioId, auth.PermView)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b.TargetId != nil && !p.Can(auth.PermRun) {
		return nil, errForbidden(fmt.Sprintf("a dry run sends requests, which needs the %s role or higher; you are %s", auth.PermRun, p.Role))
	}
	s, version, err := h.scenarioAt(ctx, sc, b.Version)
	if err != nil {
		return nil, err
	}
	curSpec, err := h.apiSpec(ctx, sc.ProjectID, b.Openapi, b.SpecURL, "openapi")
	if err != nil {
		return nil, err
	}
	if curSpec == nil {
		return nil, errInvalid("give the current API as openapi or specURL")
	}
	cur, err := understandSpec(curSpec, "openapi")
	if err != nil {
		return nil, err
	}
	prevSpec, err := h.apiSpec(ctx, sc.ProjectID, b.PreviousOpenapi, b.PreviousSpecURL, "previousOpenapi")
	if err != nil {
		return nil, err
	}

	out := gen.ScenarioDrift{Version: version}
	if prevSpec != nil {
		old, err := understandSpec(prevSpec, "previousOpenapi")
		if err != nil {
			return nil, err
		}
		d := ai.DiffSpecs(s, old, cur)
		added, removed := driftEndpoints(d.Added), driftEndpoints(d.Removed)
		out.Added, out.Removed = &added, &removed
		journeys := make([]string, 0, len(d.Broken))
		for j := range d.Broken {
			journeys = append(journeys, j)
		}
		sort.Strings(journeys)
		broken := make([]struct {
			Endpoints []gen.DriftEndpoint `json:"endpoints"`
			Journey   string              `json:"journey"`
		}, 0, len(journeys))
		for _, j := range journeys {
			broken = append(broken, struct {
				Endpoints []gen.DriftEndpoint `json:"endpoints"`
				Journey   string              `json:"journey"`
			}{Endpoints: driftEndpoints(d.Broken[j]), Journey: j})
		}
		out.Broken = &broken
		out.Drifted = len(broken) > 0
	}
	out.Unmatched = requestRefs(ai.CoverageOf(s, cur).Unmatched)
	out.Drifted = out.Drifted || len(out.Unmatched) > 0

	if b.TargetId != nil {
		checks, err := h.driftDryRun(ctx, sc, s, *b.TargetId)
		if err != nil {
			return nil, err
		}
		out.DryRun = &checks
		for _, c := range checks {
			out.Drifted = out.Drifted || !c.Ok
		}
	}
	return gen.ScenarioDrift200JSONResponse(out), nil
}

// driftDryRunTimeout bounds the dry run of all journeys.
const driftDryRunTimeout = 2 * time.Minute

// driftDryRun runs every journey once with one user against a target of
// the scenario's project, with the project's secrets.
func (h *handlers) driftDryRun(ctx context.Context, sc db.Scenario, s *scenario.Scenario, targetID uuid.UUID) ([]gen.DriftJourneyCheck, error) {
	p := auth.FromContext(ctx)
	tg, err := h.st.GetTarget(ctx, db.GetTargetParams{ID: targetID, OrgID: p.OrgID})
	if err != nil || tg.ProjectID != sc.ProjectID {
		return nil, errInvalid("target not found in this project")
	}
	if err := confineDataFiles(s, h.cfg.DataDir); err != nil {
		return nil, err
	}
	secrets, err := h.projectSecrets(ctx, sc.ProjectID)
	if err != nil {
		return nil, err
	}
	s.Target.BaseURL = tg.BaseUrl
	h.audit(ctx, "scenario.drift.dryrun", sc.Name+" → "+tg.BaseUrl, map[string]any{"scenario": sc.ID, "target": tg.ID})
	dctx, cancel := context.WithTimeout(ctx, driftDryRunTimeout)
	defer cancel()
	env := map[string]string{"TARGET_URL": tg.BaseUrl}
	checks, err := ai.DryRunScenario(dctx, s, tg.BaseUrl, tg.AllowHosts, env, secrets, h.cfg.DataDir)
	if err != nil && len(checks) == 0 {
		return nil, errInvalid("dry run: " + err.Error())
	}
	out := make([]gen.DriftJourneyCheck, 0, len(checks))
	for _, c := range checks {
		out = append(out, driftCheckOf(c))
	}
	return out, nil
}

// driftCheckOf reports a journey's dry run by its first failure, as
// stampede drift prints it.
func driftCheckOf(c ai.JourneyCheck) gen.DriftJourneyCheck {
	out := gen.DriftJourneyCheck{Journey: c.Journey, Ok: c.OK}
	if c.OK {
		return out
	}
	for _, tr := range c.Traces {
		if tr.OK {
			continue
		}
		if tr.Error != "" {
			out.Error = ptr(tr.Error)
		}
		for _, st := range tr.Steps {
			if st.OK {
				continue
			}
			out.Step = ptr(st.Step)
			if st.Error != "" && out.Error == nil {
				out.Error = ptr(st.Error)
			}
			if st.Status != 0 {
				out.Status = ptr(st.Status)
			}
			break
		}
		break
	}
	return out
}
