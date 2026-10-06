package server

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
)

// maxScenarioBytes bounds stored scenario files.
const maxScenarioBytes = 1 << 20

// analyse parses and validates YAML, returning problems as a list.
func analyse(src string) (*scenario.Scenario, *gen.PlanSummary, []string) {
	if len(src) > maxScenarioBytes {
		return nil, nil, []string{"scenario is larger than 1 MiB"}
	}
	s, err := scenario.Decode([]byte(src))
	if err == nil {
		// On the server the run's target supplies the base URL, so a stored
		// scenario may leave it out. Validate as if one were set.
		orig := s.Target.BaseURL
		if orig == "" {
			s.Target.BaseURL = "http://target.invalid"
		}
		err = s.Validate()
		s.Target.BaseURL = orig
	}
	if err != nil {
		var ve *scenario.ValidationError
		if errors.As(err, &ve) {
			return nil, nil, ve.Problems
		}
		return nil, nil, []string{err.Error()}
	}
	return s, planSummary(s), nil
}

func planSummary(s *scenario.Scenario) *gen.PlanSummary {
	plan, err := s.Load.Plan()
	if err != nil {
		return nil
	}
	prog, err := scenario.Compile(s)
	if err != nil {
		return nil
	}
	ps := &gen.PlanSummary{
		Executor: plan.Executor, Mode: plan.Mode, Peak: plan.Peak(),
		DurationSeconds: plan.TotalDuration().Seconds(), Journeys: len(prog.Journeys), Steps: len(prog.Steps),
	}
	if plan.Shape != "" {
		sh := plan.Shape
		ps.Shape = &sh
	}
	return ps
}

func (h *handlers) ValidateScenario(ctx context.Context, req gen.ValidateScenarioRequestObject) (gen.ValidateScenarioResponseObject, error) {
	if _, err := need(ctx, auth.PermView); err != nil {
		return nil, err
	}
	_, plan, problems := analyse(req.Body.Yaml)
	return gen.ValidateScenario200JSONResponse{Valid: len(problems) == 0, Problems: nonNil(problems), Plan: plan}, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func versionOf(v db.ScenarioVersion, creators map[uuid.UUID]string) gen.ScenarioVersion {
	out := gen.ScenarioVersion{ScenarioId: v.ScenarioID, Version: int(v.Version), Yaml: v.Yaml, CreatedAt: v.CreatedAt}
	if v.Message != "" {
		m := v.Message
		out.Message = &m
	}
	if v.CreatedBy != nil {
		if name, ok := creators[*v.CreatedBy]; ok {
			out.CreatedBy = &name
		}
	}
	if len(v.Plan) > 0 {
		var ps gen.PlanSummary
		if json.Unmarshal(v.Plan, &ps) == nil {
			out.Plan = &ps
		}
	}
	return out
}

func (h *handlers) creators(ctx context.Context, org uuid.UUID) map[uuid.UUID]string {
	out := map[uuid.UUID]string{}
	rows, err := h.st.ListMembers(ctx, org)
	if err == nil {
		for _, r := range rows {
			out[r.ID] = r.Email
		}
	}
	return out
}

func (h *handlers) scenarioOut(ctx context.Context, org uuid.UUID, sc db.Scenario) (gen.Scenario, error) {
	v, err := h.st.GetLatestScenarioVersion(ctx, sc.ID)
	if err != nil {
		return gen.Scenario{}, err
	}
	d := sc.Description
	return gen.Scenario{
		Id: sc.ID, ProjectId: sc.ProjectID, Name: sc.Name, Description: &d, Tags: sc.Tags,
		LatestVersion: versionOf(v, h.creators(ctx, org)), CreatedAt: sc.CreatedAt, UpdatedAt: sc.UpdatedAt,
	}, nil
}

func (h *handlers) ListScenarios(ctx context.Context, req gen.ListScenariosRequestObject) (gen.ListScenariosResponseObject, error) {
	p, pr, err := h.project(ctx, req.ProjectId, auth.PermView)
	if err != nil {
		return nil, err
	}
	rows, err := h.st.ListScenarios(ctx, pr.ID)
	if err != nil {
		return nil, err
	}
	out := gen.ListScenarios200JSONResponse{}
	for _, sc := range rows {
		if req.Params.Tag != nil && !contains(sc.Tags, *req.Params.Tag) {
			continue
		}
		o, err := h.scenarioOut(ctx, p.OrgID, sc)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (h *handlers) CreateScenario(ctx context.Context, req gen.CreateScenarioRequestObject) (gen.CreateScenarioResponseObject, error) {
	p, pr, err := h.project(ctx, req.ProjectId, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	s, plan, problems := analyse(req.Body.Yaml)
	if len(problems) > 0 {
		return nil, errInvalid("the scenario has problems", problems...)
	}
	planJSON, _ := json.Marshal(plan)
	id := uuid.New()
	msg := "created"
	if req.Body.Message != nil && *req.Body.Message != "" {
		msg = *req.Body.Message
	}
	uid := p.UserID
	err = h.st.InTx(ctx, func(q *db.Queries) error {
		if err := q.CreateScenario(ctx, db.CreateScenarioParams{
			ID: id, ProjectID: pr.ID, Name: s.Metadata.Name, Description: s.Metadata.Description, Tags: nonNil(s.Metadata.Tags),
		}); err != nil {
			return err
		}
		return q.CreateScenarioVersion(ctx, db.CreateScenarioVersionParams{
			ScenarioID: id, Version: 1, Yaml: req.Body.Yaml, Message: msg, Plan: planJSON, CreatedBy: &uid,
		})
	})
	if err != nil {
		if store.IsUniqueViolation(err) {
			return nil, errConflict("a scenario named " + s.Metadata.Name + " already exists in this project; save a new version of it instead")
		}
		return nil, err
	}
	h.audit(ctx, "scenario.create", s.Metadata.Name, nil)
	sc, err := h.st.GetScenario(ctx, db.GetScenarioParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		return nil, err
	}
	out, err := h.scenarioOut(ctx, p.OrgID, sc)
	if err != nil {
		return nil, err
	}
	return gen.CreateScenario201JSONResponse(out), nil
}

func (h *handlers) scenario(ctx context.Context, id uuid.UUID, min auth.Role) (*auth.Principal, db.Scenario, error) {
	p, err := need(ctx, min)
	if err != nil {
		return nil, db.Scenario{}, err
	}
	sc, err := h.st.GetScenario(ctx, db.GetScenarioParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		return nil, db.Scenario{}, notFoundOr(err, "scenario")
	}
	return p, sc, nil
}

func (h *handlers) GetScenario(ctx context.Context, req gen.GetScenarioRequestObject) (gen.GetScenarioResponseObject, error) {
	p, sc, err := h.scenario(ctx, req.ScenarioId, auth.PermView)
	if err != nil {
		return nil, err
	}
	out, err := h.scenarioOut(ctx, p.OrgID, sc)
	if err != nil {
		return nil, err
	}
	return gen.GetScenario200JSONResponse(out), nil
}

func (h *handlers) DeleteScenario(ctx context.Context, req gen.DeleteScenarioRequestObject) (gen.DeleteScenarioResponseObject, error) {
	p, sc, err := h.scenario(ctx, req.ScenarioId, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	if h.runs.scenarioHasActiveRun(sc.ID) {
		return nil, errConflict("a run of this scenario is active")
	}
	if _, err := h.st.DeleteScenario(ctx, db.DeleteScenarioParams{ID: sc.ID, OrgID: p.OrgID}); err != nil {
		return nil, err
	}
	h.audit(ctx, "scenario.delete", sc.Name, nil)
	return gen.DeleteScenario204Response{}, nil
}

func (h *handlers) ListScenarioVersions(ctx context.Context, req gen.ListScenarioVersionsRequestObject) (gen.ListScenarioVersionsResponseObject, error) {
	p, sc, err := h.scenario(ctx, req.ScenarioId, auth.PermView)
	if err != nil {
		return nil, err
	}
	rows, err := h.st.ListScenarioVersions(ctx, sc.ID)
	if err != nil {
		return nil, err
	}
	cr := h.creators(ctx, p.OrgID)
	out := gen.ListScenarioVersions200JSONResponse{}
	for _, v := range rows {
		out = append(out, versionOf(v, cr))
	}
	return out, nil
}

func (h *handlers) CreateScenarioVersion(ctx context.Context, req gen.CreateScenarioVersionRequestObject) (gen.CreateScenarioVersionResponseObject, error) {
	p, sc, err := h.scenario(ctx, req.ScenarioId, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	s, plan, problems := analyse(req.Body.Yaml)
	if len(problems) > 0 {
		return nil, errInvalid("the scenario has problems", problems...)
	}
	planJSON, _ := json.Marshal(plan)
	msg := ""
	if req.Body.Message != nil {
		msg = *req.Body.Message
	}
	uid := p.UserID
	var version int32
	// The lock keyed on the scenario stops two saves taking the same number.
	err = h.st.InTxLocked(ctx, int64(sc.ID.ID()), func(q *db.Queries) error {
		next, err := q.NextScenarioVersion(ctx, sc.ID)
		if err != nil {
			return err
		}
		version = next
		if err := q.CreateScenarioVersion(ctx, db.CreateScenarioVersionParams{
			ScenarioID: sc.ID, Version: version, Yaml: req.Body.Yaml, Message: msg, Plan: planJSON, CreatedBy: &uid,
		}); err != nil {
			return err
		}
		return q.UpdateScenarioMeta(ctx, db.UpdateScenarioMetaParams{ID: sc.ID, Name: s.Metadata.Name, Description: s.Metadata.Description, Tags: nonNil(s.Metadata.Tags)})
	})
	if err != nil {
		if store.IsUniqueViolation(err) {
			return nil, errConflict("another scenario in this project is already named " + s.Metadata.Name)
		}
		return nil, err
	}
	h.audit(ctx, "scenario.version", sc.Name, map[string]any{"version": version})
	v, err := h.st.GetScenarioVersion(ctx, db.GetScenarioVersionParams{ScenarioID: sc.ID, Version: version})
	if err != nil {
		return nil, err
	}
	return gen.CreateScenarioVersion201JSONResponse(versionOf(v, h.creators(ctx, p.OrgID))), nil
}

func (h *handlers) GetScenarioVersion(ctx context.Context, req gen.GetScenarioVersionRequestObject) (gen.GetScenarioVersionResponseObject, error) {
	p, sc, err := h.scenario(ctx, req.ScenarioId, auth.PermView)
	if err != nil {
		return nil, err
	}
	v, err := h.st.GetScenarioVersion(ctx, db.GetScenarioVersionParams{ScenarioID: sc.ID, Version: int32(req.Version)}) //nolint:gosec // versions are small
	if err != nil {
		return nil, notFoundOr(err, "version")
	}
	return gen.GetScenarioVersion200JSONResponse(versionOf(v, h.creators(ctx, p.OrgID))), nil
}
