package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/ai"
	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/notify"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
)

// Scheduled drift checks: a schedule of kind drift starts no load. On its
// cron it dry-runs each journey of the scenario once with one user against
// the target (the same dry run as stampede drift --target), optionally
// compares the scenario with the API's current OpenAPI document, records
// a drift result and notifies drift.detected subscribers when journeys
// broke. A result can then be handed to the AI generator for a repair
// proposal (POST /drift-results/{id}/repair), which a person approves.

const (
	scheduleKindRun   = "run"
	scheduleKindDrift = "drift"

	driftOK      = "ok"
	driftDrifted = "drifted"
	driftError   = "error"
)

// DriftCheckTimeout bounds one scheduled drift check's dry run.
const DriftCheckTimeout = 3 * time.Minute

// maxDriftSpec bounds the OpenAPI document a drift check fetches.
const maxDriftSpec = 5 << 20

// driftOutcome is the stored form of a check (drift_results.result).
type driftOutcome struct {
	Journeys  []driftJourney `json:"journeys"`
	Removed   []string       `json:"removed,omitempty"`
	Added     []string       `json:"added,omitempty"`
	Unmatched []string       `json:"unmatched,omitempty"`
}

type driftJourney struct {
	Journey string     `json:"journey"`
	OK      bool       `json:"ok"`
	Problem string     `json:"problem,omitempty"`
	Traces  []ai.Trace `json:"traces,omitempty"`
}

// driftSpec is the part of a fetched spec the next check diffs against.
type driftSpec struct {
	BasePath  string        `json:"basePath,omitempty"`
	Endpoints []ai.Endpoint `json:"endpoints"`
}

// driftInput is a validated drift check, ready to run.
type driftInput struct {
	sc      db.Scenario
	tg      db.Target
	version int32
	s       *scenario.Scenario
	spec    *url.URL
	env     map[string]string
	secrets map[string]string
}

// prepareDrift loads and checks what a drift check needs: the scenario's
// latest version and the target, both in the project, and the spec URL.
func (h *handlers) prepareDrift(ctx context.Context, org uuid.UUID, pr db.Project, sp scheduleSpec) (*driftInput, error) {
	sc, err := h.st.GetScenario(ctx, db.GetScenarioParams{ID: sp.scenarioID, OrgID: org})
	if err != nil || sc.ProjectID != pr.ID {
		return nil, errInvalid("scenario not found in this project")
	}
	tg, err := h.st.GetTarget(ctx, db.GetTargetParams{ID: sp.targetID, OrgID: org})
	if err != nil || tg.ProjectID != pr.ID {
		return nil, errInvalid("target not found in this project")
	}
	ver, err := h.st.GetLatestScenarioVersion(ctx, sc.ID)
	if err != nil {
		return nil, notFoundOr(err, "scenario version")
	}
	s, err := scenario.Decode([]byte(ver.Yaml))
	if err != nil {
		return nil, errInvalid(err.Error())
	}
	if s.Load.Mode == scenario.ModeReplay {
		return nil, errInvalid("a replay scenario has no journeys to dry-run, so it cannot be checked for drift")
	}
	if err := confineDataFiles(s, h.cfg.DataDir); err != nil {
		return nil, err
	}
	s.Target.BaseURL = tg.BaseUrl
	if err := s.Validate(); err != nil {
		var ve *scenario.ValidationError
		if errors.As(err, &ve) {
			return nil, errInvalid("the scenario has problems", ve.Problems...)
		}
		return nil, errInvalid(err.Error())
	}
	in := &driftInput{sc: sc, tg: tg, version: ver.Version, s: s, env: sp.env}
	if sp.specURL != "" {
		if in.spec, err = checkSpecURL(sp.specURL, tg); err != nil {
			return nil, err
		}
	}
	if in.secrets, err = h.projectSecrets(ctx, pr.ID); err != nil {
		return nil, err
	}
	if in.env == nil {
		in.env = map[string]string{}
	}
	return in, nil
}

// checkSpecURL allows only http(s) URLs on the target's host or one of its
// allowed hosts, so a schedule cannot make the server fetch arbitrary URLs.
func checkSpecURL(raw string, tg db.Target) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errInvalid("specURL must be an absolute http(s) URL")
	}
	h := strings.ToLower(u.Hostname())
	if h != tg.Host && !slices.Contains(tg.AllowHosts, h) {
		return nil, errInvalid(fmt.Sprintf("specURL must be on the target's host %s or one of its allowed hosts, not %s", tg.Host, h))
	}
	if cat := ai.ThirdPartyCategory(h); cat != "" {
		return nil, errInvalid(fmt.Sprintf("specURL is on %s, a %s provider", h, cat))
	}
	return u, nil
}

// fetchSpec downloads an OpenAPI document, following redirects only on
// the same host.
func fetchSpec(ctx context.Context, u *url.URL) ([]byte, error) {
	c := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !strings.EqualFold(req.URL.Hostname(), u.Hostname()) {
			return errors.New("redirect to another host refused")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/yaml, application/json;q=0.9, */*;q=0.5")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxDriftSpec+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxDriftSpec {
		return nil, fmt.Errorf("the document is larger than %d MiB", maxDriftSpec>>20)
	}
	return b, nil
}

// runDrift runs one drift check of a schedule and records it. The check
// itself failing (a bad spec URL, a scenario that no longer validates) is
// recorded as a result with status error rather than returned.
func (h *handlers) runDrift(ctx context.Context, org uuid.UUID, pr db.Project, row db.GetScheduleRow, by *uuid.UUID) (db.GetDriftResultRow, error) {
	sp := specOf(row)
	out := driftOutcome{Journeys: []driftJourney{}}
	broken := map[string]bool{}
	status, errMsg := driftOK, ""
	var specJSON []byte
	version := int32(0)

	in, err := h.prepareDrift(ctx, org, pr, sp)
	if err != nil {
		status, errMsg = driftError, describe(err)
	} else {
		version = in.version
		dctx, cancel := context.WithTimeout(ctx, DriftCheckTimeout)
		checks, derr := ai.DryRunScenario(dctx, in.s, in.tg.BaseUrl, in.tg.AllowHosts, in.env, in.secrets, h.cfg.DataDir)
		cancel()
		for _, c := range checks {
			j := driftJourney{Journey: c.Journey, OK: c.OK, Traces: c.Traces}
			if !c.OK {
				j.Problem = c.Problem()
				broken[c.Journey] = true
			}
			out.Journeys = append(out.Journeys, j)
		}
		var problems []string
		if derr != nil {
			problems = append(problems, "the dry run did not finish: "+derr.Error())
		}
		if in.spec != nil {
			spec, unmatched, removed, added, serr := h.compareSpec(ctx, row.ID, in)
			if serr != nil {
				problems = append(problems, "the spec could not be compared: "+serr.Error())
			} else {
				specJSON, _ = json.Marshal(spec)
				for _, r := range unmatched {
					out.Unmatched = append(out.Unmatched, fmt.Sprintf("%s: %s %s", r.Journey, r.Method, r.URL))
					broken[r.Journey] = true
				}
				for j := range removed.Broken {
					broken[j] = true
				}
				for _, e := range removed.Removed {
					out.Removed = append(out.Removed, e.Key())
				}
				for _, e := range added {
					out.Added = append(out.Added, e.Key())
				}
			}
		}
		switch {
		case len(broken) > 0:
			status = driftDrifted
		case len(problems) > 0:
			status = driftError
		}
		errMsg = strings.Join(problems, "; ")
	}
	names := make([]string, 0, len(broken))
	for j := range broken {
		names = append(names, j)
	}
	sort.Strings(names)
	resJSON, _ := json.Marshal(out)
	id := uuid.New()
	sid := row.ID
	err = h.st.CreateDriftResult(ctx, db.CreateDriftResultParams{
		ID: id, ProjectID: pr.ID, ScheduleID: &sid, ScenarioID: row.ScenarioID, ScenarioVersion: version, TargetID: row.TargetID,
		Status: status, Error: errMsg, Broken: names, Result: resJSON, Spec: specJSON, CreatedBy: by,
	})
	if err != nil {
		return db.GetDriftResultRow{}, err
	}
	if err := h.st.RecordScheduleDrift(ctx, db.RecordScheduleDriftParams{ID: row.ID, LastDriftID: &id, LastFiredAt: ptr(h.cfg.Now())}); err != nil {
		h.log.Error("record schedule drift", "schedule", row.ID, "error", err)
	}
	res, err := h.st.GetDriftResult(ctx, db.GetDriftResultParams{ID: id, OrgID: org})
	if err != nil {
		return db.GetDriftResultRow{}, err
	}
	h.log.Info("drift check", "schedule", row.ID, "name", row.Name, "status", status, "broken", names)
	if status == driftDrifted {
		h.notifyDrift(org, pr, res)
	}
	return res, nil
}

// compareSpec fetches the schedule's spec and compares it with the
// scenario (requests that match no endpoint) and with the spec the
// schedule's previous check fetched (endpoints removed and added).
func (h *handlers) compareSpec(ctx context.Context, schedule uuid.UUID, in *driftInput) (driftSpec, []ai.RequestRef, *ai.SpecDiff, []ai.Endpoint, error) {
	body, err := fetchSpec(ctx, in.spec)
	if err != nil {
		return driftSpec{}, nil, nil, nil, err
	}
	und, err := ai.Understand(ai.Inputs{OpenAPI: body}, ai.NewRedactor())
	if err != nil {
		return driftSpec{}, nil, nil, nil, err
	}
	spec := driftSpec{BasePath: und.BasePath, Endpoints: und.Endpoints}
	unmatched := ai.CoverageOf(in.s, und).Unmatched
	diff := &ai.SpecDiff{}
	var added []ai.Endpoint
	prevJSON, err := h.st.LastDriftSpec(ctx, &schedule)
	switch {
	case err == nil:
		var prev driftSpec
		if json.Unmarshal(prevJSON, &prev) == nil {
			diff = ai.DiffSpecs(in.s, &ai.Understanding{Endpoints: prev.Endpoints, BasePath: prev.BasePath, HasSpec: true}, und)
			added = diff.Added
		}
	case !store.IsNotFound(err):
		return driftSpec{}, nil, nil, nil, err
	}
	return spec, unmatched, diff, added, nil
}

// notifyDrift tells drift.detected subscribers which journeys broke.
func (h *handlers) notifyDrift(org uuid.UUID, pr db.Project, r db.GetDriftResultRow) {
	d := &notify.Drift{ID: r.ID.String(), Project: pr.Name, Scenario: r.ScenarioName, Target: r.TargetUrl, Broken: r.Broken}
	if r.ScheduleName != nil {
		d.Schedule = *r.ScheduleName
	}
	if h.cfg.Notify.PublicURL != "" {
		d.URL = strings.TrimRight(h.cfg.Notify.PublicURL, "/") + "/api/v1/drift-results/" + r.ID.String()
	}
	orgName := ""
	if o, err := h.st.GetOrg(context.Background(), org); err == nil {
		orgName = o.Name
	}
	msg := fmt.Sprintf("%s: %d journeys broke against %s (%s)", r.ScenarioName, len(r.Broken), r.TargetUrl, strings.Join(r.Broken, ", "))
	h.notify.publish(org, notify.Event{Type: notify.EventDriftDetected, At: time.Now().UTC(), Org: orgName, Message: msg, Drift: d}, nil)
}

// fireDrift runs a claimed drift schedule's check as its owner.
func (s *Server) fireDrift(ctx context.Context, h *handlers, org uuid.UUID, pr db.Project, row db.GetScheduleRow, owner uuid.UUID) {
	if _, err := h.runDrift(ctx, org, pr, row, &owner); err != nil {
		s.log.Error("drift check", "schedule", row.ID, "error", err)
	}
}

func driftResultOf(r db.GetDriftResultRow, traces bool) gen.DriftResult {
	out := gen.DriftResult{
		Id: r.ID, ProjectId: r.ProjectID, ScheduleId: r.ScheduleID, ScheduleName: r.ScheduleName, ScenarioId: r.ScenarioID,
		ScenarioName: &r.ScenarioName, ScenarioVersion: int(r.ScenarioVersion), TargetId: r.TargetID, TargetURL: &r.TargetUrl,
		Status: gen.DriftResultStatus(r.Status), Broken: r.Broken, RepairJobId: r.RepairJobID, CreatedAt: r.CreatedAt,
	}
	if out.Broken == nil {
		out.Broken = []string{}
	}
	if r.Error != "" {
		e := r.Error
		out.Error = &e
	}
	var o driftOutcome
	if json.Unmarshal(r.Result, &o) == nil {
		js := []gen.DriftJourney{}
		for _, j := range o.Journeys {
			gj := gen.DriftJourney{Journey: j.Journey, Ok: j.OK}
			if j.Problem != "" {
				p := j.Problem
				gj.Problem = &p
			}
			if traces && len(j.Traces) > 0 {
				var ts []gen.AITrace
				if b, err := json.Marshal(j.Traces); err == nil && json.Unmarshal(b, &ts) == nil {
					gj.Traces = &ts
				}
			}
			js = append(js, gj)
		}
		out.Journeys = &js
		if len(o.Removed) > 0 {
			out.RemovedEndpoints = &o.Removed
		}
		if len(o.Added) > 0 {
			out.AddedEndpoints = &o.Added
		}
		if len(o.Unmatched) > 0 {
			out.Unmatched = &o.Unmatched
		}
	}
	return out
}

func (h *handlers) ListDriftResults(ctx context.Context, req gen.ListDriftResultsRequestObject) (gen.ListDriftResultsResponseObject, error) {
	_, pr, err := h.project(ctx, req.ProjectId, auth.PermView)
	if err != nil {
		return nil, err
	}
	limit := int32(50)
	if req.Params.Limit != nil {
		limit = int32(min(max(*req.Params.Limit, 1), 200)) //nolint:gosec // bounded
	}
	rows, err := h.st.ListDriftResults(ctx, db.ListDriftResultsParams{ProjectID: pr.ID, ScheduleID: req.Params.ScheduleId, Lim: limit})
	if err != nil {
		return nil, err
	}
	out := gen.ListDriftResults200JSONResponse{}
	for _, r := range rows {
		out = append(out, driftResultOf(db.GetDriftResultRow{
			ID: r.ID, ProjectID: r.ProjectID, ScheduleID: r.ScheduleID, ScenarioID: r.ScenarioID, ScenarioVersion: r.ScenarioVersion,
			TargetID: r.TargetID, Status: r.Status, Error: r.Error, Broken: r.Broken, RepairJobID: r.RepairJobID, CreatedAt: r.CreatedAt,
			ScenarioName: r.ScenarioName, TargetUrl: r.TargetUrl, ScheduleName: r.ScheduleName,
		}, false))
	}
	return out, nil
}

// driftResult loads a drift result and checks the caller's role in its
// project.
func (h *handlers) driftResult(ctx context.Context, id uuid.UUID, min auth.Role) (db.GetDriftResultRow, error) {
	p, err := need(ctx, auth.PermView)
	if err != nil {
		return db.GetDriftResultRow{}, err
	}
	r, err := h.st.GetDriftResult(ctx, db.GetDriftResultParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		return db.GetDriftResultRow{}, notFoundOr(err, "drift result")
	}
	if _, err := h.needIn(ctx, r.ProjectID, min); err != nil {
		return db.GetDriftResultRow{}, err
	}
	return r, nil
}

func (h *handlers) GetDriftResult(ctx context.Context, req gen.GetDriftResultRequestObject) (gen.GetDriftResultResponseObject, error) {
	r, err := h.driftResult(ctx, req.DriftId, auth.PermView)
	if err != nil {
		return nil, err
	}
	return gen.GetDriftResult200JSONResponse(driftResultOf(r, true)), nil
}

// repairBrief tells the model what broke, with the check's (redacted)
// findings, and to leave the journeys that still work alone.
func repairBrief(r db.GetDriftResultRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The scenario %q (version %d) has drifted from the API it tests: a scheduled drift check against %s found journeys that no longer work. "+
		"Repair only the broken journeys (%s) so they work against the current API, and keep every other journey, the load section and the targets exactly as they are.\n\n",
		r.ScenarioName, r.ScenarioVersion, r.TargetUrl, strings.Join(r.Broken, ", "))
	var o driftOutcome
	if json.Unmarshal(r.Result, &o) == nil {
		for _, j := range o.Journeys {
			if !j.OK {
				fmt.Fprintf(&b, "- journey %s failed its dry run: %s\n", j.Journey, j.Problem)
			}
		}
		for _, e := range o.Removed {
			fmt.Fprintf(&b, "- endpoint %s was removed from the API\n", e)
		}
		for _, u := range o.Unmatched {
			fmt.Fprintf(&b, "- request %s matches no endpoint of the current API\n", u)
		}
		if len(o.Added) > 0 {
			fmt.Fprintf(&b, "- endpoints added since the previous check: %s\n", strings.Join(o.Added, ", "))
		}
	}
	return ai.Truncate(b.String(), maxAIDescription)
}

// RepairDrift starts an AI generation job that proposes a repaired version
// of a drifted scenario. The proposal is saved only on approval.
func (h *handlers) RepairDrift(ctx context.Context, req gen.RepairDriftRequestObject) (gen.RepairDriftResponseObject, error) {
	r, err := h.driftResult(ctx, req.DriftId, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	if r.Status != driftDrifted || len(r.Broken) == 0 {
		return nil, errConflict("this drift check found no broken journeys to repair")
	}
	p, pr, err := h.project(ctx, r.ProjectID, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	in := ai.Inputs{Description: repairBrief(r)}
	// The spec the schedule checks against, fetched again so the model
	// sees the API as it is now. Without it the model works from the
	// scenario and the dry-run evidence alone.
	if r.ScheduleID != nil {
		if row, err := h.st.GetSchedule(ctx, db.GetScheduleParams{ID: *r.ScheduleID, OrgID: p.OrgID}); err == nil && row.SpecUrl != "" {
			if tg, err := h.st.GetTarget(ctx, db.GetTargetParams{ID: r.TargetID, OrgID: p.OrgID}); err == nil {
				if u, err := checkSpecURL(row.SpecUrl, tg); err == nil {
					if body, err := fetchSpec(ctx, u); err == nil {
						in.OpenAPI = body
					}
				}
			}
		}
	}
	jr := aiJobRequest{inputs: in, targetID: &r.TargetID, scenarioID: &r.ScenarioID, dryRun: true, purpose: "drift repair " + r.ID.String()}
	if b := req.Body; b != nil {
		jr.providerID, jr.maxRepairs = b.ProviderId, b.MaxRepairs
	}
	job, err := h.launchAIJob(ctx, p, pr, jr)
	if err != nil {
		return nil, err
	}
	if err := h.st.SetDriftRepairJob(ctx, db.SetDriftRepairJobParams{ID: r.ID, RepairJobID: &job.ID}); err != nil {
		return nil, err
	}
	return gen.RepairDrift202JSONResponse(aiJobOf(job, h.creators(ctx, p.OrgID))), nil
}
