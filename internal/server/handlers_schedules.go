package server

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/cron"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
)

func scheduleOf(r db.GetScheduleRow) gen.Schedule {
	out := gen.Schedule{
		Id: r.ID, ProjectId: r.ProjectID, Name: r.Name, ScenarioId: r.ScenarioID, ScenarioName: &r.ScenarioName,
		TargetId: r.TargetID, TargetName: &r.TargetName, Cron: r.Cron, Timezone: r.Timezone, Workers: int(r.Workers),
		Enabled: r.Enabled, Note: &r.Note, OwnerId: r.OwnerID, OwnerEmail: r.OwnerEmail, CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt, NextRunAt: r.NextRunAt, LastFiredAt: r.LastFiredAt, LastRunId: r.LastRunID,
		LastRunAt: r.LastRunAt, LastRunVerdict: r.LastRunVerdict, LastSkipReason: r.LastSkipReason,
	}
	if r.LastRunStatus != nil {
		st := gen.RunStatus(*r.LastRunStatus)
		out.LastRunStatus = &st
	}
	var ov gen.RunOverrides
	if json.Unmarshal(r.Overrides, &ov) == nil {
		out.Overrides = &ov
	}
	env := map[string]string{}
	if json.Unmarshal(r.Env, &env) == nil {
		out.Env = &env
	}
	return out
}

// scheduleSpec is a schedule's editable fields, validated.
type scheduleSpec struct {
	name, cron, timezone, note string
	scenarioID, targetID       uuid.UUID
	overrides                  gen.RunOverrides
	env                        map[string]string
	workers                    int
	enabled                    bool
}

func (sp scheduleSpec) runInput() runInput {
	ov := sp.overrides
	return runInput{
		scenarioID: sp.scenarioID, targetID: sp.targetID, overrides: &ov, env: maps.Clone(sp.env),
		workers: sp.workers, note: "scheduled: " + sp.name,
	}
}

// parseCron checks an expression and zone and returns the first firing
// after now. An empty zone is UTC.
func parseCron(expr, tz string, now time.Time) (*cron.Schedule, time.Time, error) {
	if tz == "" {
		tz = "UTC"
	}
	cs, err := cron.Parse(expr, tz)
	if err != nil {
		return nil, time.Time{}, errInvalid("cron: " + err.Error())
	}
	next := cs.Next(now)
	if next.IsZero() {
		return nil, time.Time{}, errInvalid("cron: the expression does not fire in the next nine years")
	}
	return cs, next, nil
}

// check validates a schedule. With full set it also prepares (but does not
// start) its run, so problems show up when the schedule is saved rather
// than at 2am.
func (h *handlers) checkSchedule(ctx context.Context, org uuid.UUID, pr db.Project, sp *scheduleSpec, full bool) (time.Time, error) {
	sp.name = strings.TrimSpace(sp.name)
	if sp.name == "" {
		return time.Time{}, errInvalid("name is required")
	}
	if len(sp.name) > 100 {
		return time.Time{}, errInvalid("name is longer than 100 characters")
	}
	if len(sp.note) > 500 {
		return time.Time{}, errInvalid("note is longer than 500 characters")
	}
	if sp.workers < 0 {
		return time.Time{}, errInvalid("workers cannot be negative")
	}
	if sp.timezone == "" {
		sp.timezone = "UTC"
	}
	_, next, err := parseCron(sp.cron, sp.timezone, h.cfg.Now())
	if err != nil {
		return time.Time{}, err
	}
	sp.cron = strings.TrimSpace(sp.cron)
	if full {
		if _, err := h.prepareRun(ctx, org, pr, sp.runInput()); err != nil {
			return time.Time{}, err
		}
	}
	return next, nil
}

func (h *handlers) schedule(ctx context.Context, id uuid.UUID, min auth.Role) (*auth.Principal, db.GetScheduleRow, error) {
	p, err := need(ctx, min)
	if err != nil {
		return nil, db.GetScheduleRow{}, err
	}
	row, err := h.st.GetSchedule(ctx, db.GetScheduleParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		return nil, db.GetScheduleRow{}, notFoundOr(err, "schedule")
	}
	return p, row, nil
}

func (h *handlers) ListSchedules(ctx context.Context, req gen.ListSchedulesRequestObject) (gen.ListSchedulesResponseObject, error) {
	_, pr, err := h.project(ctx, req.ProjectId, auth.PermView)
	if err != nil {
		return nil, err
	}
	rows, err := h.st.ListSchedules(ctx, pr.ID)
	if err != nil {
		return nil, err
	}
	out := gen.ListSchedules200JSONResponse{}
	for _, r := range rows {
		out = append(out, scheduleOf(db.GetScheduleRow(r)))
	}
	return out, nil
}

func (h *handlers) GetSchedule(ctx context.Context, req gen.GetScheduleRequestObject) (gen.GetScheduleResponseObject, error) {
	_, row, err := h.schedule(ctx, req.ScheduleId, auth.PermView)
	if err != nil {
		return nil, err
	}
	return gen.GetSchedule200JSONResponse(scheduleOf(row)), nil
}

func (h *handlers) CreateSchedule(ctx context.Context, req gen.CreateScheduleRequestObject) (gen.CreateScheduleResponseObject, error) {
	p, pr, err := h.project(ctx, req.ProjectId, auth.PermEditSchedules)
	if err != nil {
		return nil, err
	}
	b := req.Body
	sp := scheduleSpec{name: b.Name, cron: b.Cron, scenarioID: b.ScenarioId, targetID: b.TargetId, enabled: true}
	if b.Timezone != nil {
		sp.timezone = strings.TrimSpace(*b.Timezone)
	}
	if b.Overrides != nil {
		sp.overrides = *b.Overrides
	}
	if b.Env != nil {
		sp.env = *b.Env
	}
	if b.Workers != nil {
		sp.workers = *b.Workers
	}
	if b.Enabled != nil {
		sp.enabled = *b.Enabled
	}
	if b.Note != nil {
		sp.note = *b.Note
	}
	next, err := h.checkSchedule(ctx, p.OrgID, pr, &sp, true)
	if err != nil {
		return nil, err
	}
	id, uid := uuid.New(), p.UserID
	ovJSON, envJSON := scheduleJSON(sp)
	var nextAt *time.Time
	if sp.enabled {
		nextAt = &next
	}
	err = h.st.CreateSchedule(ctx, db.CreateScheduleParams{
		ID: id, ProjectID: pr.ID, Name: sp.name, ScenarioID: sp.scenarioID, TargetID: sp.targetID, Cron: sp.cron,
		Timezone: sp.timezone, Overrides: ovJSON, Env: envJSON, Workers: int32(sp.workers), Enabled: sp.enabled, //nolint:gosec // small
		Note: sp.note, OwnerID: &uid, NextRunAt: nextAt,
	})
	if err != nil {
		if store.IsUniqueViolation(err) {
			return nil, errConflict(fmt.Sprintf("a schedule named %q already exists in this project", sp.name))
		}
		return nil, err
	}
	h.audit(ctx, "schedule.create", sp.name, map[string]any{"schedule": id, "cron": sp.cron, "timezone": sp.timezone, "enabled": sp.enabled})
	row, err := h.st.GetSchedule(ctx, db.GetScheduleParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		return nil, err
	}
	return gen.CreateSchedule201JSONResponse(scheduleOf(row)), nil
}

func scheduleJSON(sp scheduleSpec) (ov, env []byte) {
	ov, _ = json.Marshal(sp.overrides)
	if sp.env == nil {
		sp.env = map[string]string{}
	}
	env, _ = json.Marshal(sp.env)
	return ov, env
}

func (h *handlers) UpdateSchedule(ctx context.Context, req gen.UpdateScheduleRequestObject) (gen.UpdateScheduleResponseObject, error) {
	p, row, err := h.schedule(ctx, req.ScheduleId, auth.PermEditSchedules)
	if err != nil {
		return nil, err
	}
	pr, err := h.st.GetProject(ctx, db.GetProjectParams{ID: row.ProjectID, OrgID: p.OrgID})
	if err != nil {
		return nil, err
	}
	sp := specOf(row)
	b := req.Body
	onlyToggle := b.Enabled != nil && b.Name == nil && b.Cron == nil && b.Timezone == nil && b.Note == nil &&
		b.ScenarioId == nil && b.TargetId == nil && b.Overrides == nil && b.Env == nil && b.Workers == nil
	if b.Name != nil {
		sp.name = *b.Name
	}
	if b.Cron != nil {
		sp.cron = *b.Cron
	}
	if b.Timezone != nil {
		sp.timezone = strings.TrimSpace(*b.Timezone)
	}
	if b.Note != nil {
		sp.note = *b.Note
	}
	if b.ScenarioId != nil {
		sp.scenarioID = *b.ScenarioId
	}
	if b.TargetId != nil {
		sp.targetID = *b.TargetId
	}
	if b.Overrides != nil {
		sp.overrides = *b.Overrides
	}
	if b.Env != nil {
		sp.env = *b.Env
	}
	if b.Workers != nil {
		sp.workers = *b.Workers
	}
	if b.Enabled != nil {
		sp.enabled = *b.Enabled
	}
	// Disabling a schedule is always allowed, even one whose run would no
	// longer be accepted; anything else is checked in full.
	full := !onlyToggle || sp.enabled
	next, err := h.checkSchedule(ctx, p.OrgID, pr, &sp, full)
	if err != nil {
		return nil, err
	}
	nextAt := row.NextRunAt
	switch {
	case !sp.enabled:
		nextAt = nil
	case nextAt == nil || sp.cron != row.Cron || sp.timezone != row.Timezone:
		nextAt = &next
	}
	ovJSON, envJSON := scheduleJSON(sp)
	uid := p.UserID
	err = h.st.UpdateSchedule(ctx, db.UpdateScheduleParams{
		ID: row.ID, Name: sp.name, ScenarioID: sp.scenarioID, TargetID: sp.targetID, Cron: sp.cron, Timezone: sp.timezone,
		Overrides: ovJSON, Env: envJSON, Workers: int32(sp.workers), Enabled: sp.enabled, Note: sp.note, //nolint:gosec // small
		OwnerID: &uid, NextRunAt: nextAt,
	})
	if err != nil {
		if store.IsUniqueViolation(err) {
			return nil, errConflict(fmt.Sprintf("a schedule named %q already exists in this project", sp.name))
		}
		return nil, err
	}
	action := "schedule.update"
	if onlyToggle {
		action = map[bool]string{true: "schedule.enable", false: "schedule.disable"}[sp.enabled]
	}
	h.audit(ctx, action, sp.name, map[string]any{"schedule": row.ID, "cron": sp.cron, "timezone": sp.timezone, "enabled": sp.enabled})
	row, err = h.st.GetSchedule(ctx, db.GetScheduleParams{ID: row.ID, OrgID: p.OrgID})
	if err != nil {
		return nil, err
	}
	return gen.UpdateSchedule200JSONResponse(scheduleOf(row)), nil
}

func (h *handlers) DeleteSchedule(ctx context.Context, req gen.DeleteScheduleRequestObject) (gen.DeleteScheduleResponseObject, error) {
	_, row, err := h.schedule(ctx, req.ScheduleId, auth.PermEditSchedules)
	if err != nil {
		return nil, err
	}
	if _, err := h.st.DeleteSchedule(ctx, row.ID); err != nil {
		return nil, err
	}
	h.audit(ctx, "schedule.delete", row.Name, map[string]any{"schedule": row.ID})
	return gen.DeleteSchedule204Response{}, nil
}

// RunSchedule starts a schedule's run now, as the caller. It needs only
// the runner role: anyone who may start runs may start this one.
func (h *handlers) RunSchedule(ctx context.Context, req gen.RunScheduleRequestObject) (gen.RunScheduleResponseObject, error) {
	p, row, err := h.schedule(ctx, req.ScheduleId, auth.PermRun)
	if err != nil {
		return nil, err
	}
	if active(row.LastRunStatus) {
		return nil, errConflict("the schedule's previous run is still active")
	}
	pr, err := h.st.GetProject(ctx, db.GetProjectParams{ID: row.ProjectID, OrgID: p.OrgID})
	if err != nil {
		return nil, err
	}
	prep, err := h.prepareRun(ctx, p.OrgID, pr, specOf(row).runInput())
	if err != nil {
		return nil, err
	}
	run, err := h.startRun(ctx, p, pr, prep)
	if err != nil {
		return nil, err
	}
	if err := h.st.RecordScheduleRun(ctx, db.RecordScheduleRunParams{ID: row.ID, LastRunID: &run.ID, LastFiredAt: ptr(h.cfg.Now())}); err != nil {
		h.log.Error("record schedule run", "schedule", row.ID, "error", err)
	}
	return gen.RunSchedule201JSONResponse(runOf(run)), nil
}

func specOf(row db.GetScheduleRow) scheduleSpec {
	s := scheduleOf(row)
	return scheduleSpec{
		name: row.Name, cron: row.Cron, timezone: row.Timezone, note: row.Note, scenarioID: row.ScenarioID,
		targetID: row.TargetID, overrides: *s.Overrides, env: *s.Env, workers: int(row.Workers), enabled: row.Enabled,
	}
}

func active(status *string) bool {
	if status == nil {
		return false
	}
	switch *status {
	case statusScheduling, statusStarting, statusRunning, statusStopping, statusAnalyzing:
		return true
	}
	return false
}

func (h *handlers) PreviewSchedule(ctx context.Context, req gen.PreviewScheduleRequestObject) (gen.PreviewScheduleResponseObject, error) {
	if _, err := need(ctx, auth.PermView); err != nil {
		return nil, err
	}
	tz := ""
	if req.Params.Timezone != nil {
		tz = strings.TrimSpace(*req.Params.Timezone)
	}
	cs, _, err := parseCron(req.Params.Cron, tz, h.cfg.Now())
	if err != nil {
		return nil, err
	}
	n := 3
	if req.Params.Count != nil {
		n = min(max(*req.Params.Count, 1), 20)
	}
	out := gen.PreviewSchedule200JSONResponse{Timezone: cs.Location().String(), Next: []time.Time{}}
	for _, t := range cs.NextN(h.cfg.Now(), n) {
		out.Next = append(out.Next, t.In(cs.Location()))
	}
	return out, nil
}
