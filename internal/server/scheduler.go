package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/cron"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
)

// DefaultSchedulerInterval is how often the scheduler looks for due
// schedules unless Config.SchedulerInterval says otherwise.
const DefaultSchedulerInterval = 15 * time.Second

type scheduler struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// StartScheduler starts the loop that fires due schedules, and returns at
// once. Call it only on the active replica (after Recover). It checks
// straight away, so firings missed while no server was running start once
// on start-up, then every Config.SchedulerInterval. Shutdown stops it.
func (s *Server) StartScheduler(ctx context.Context) {
	if s.sched != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	sc := &scheduler{cancel: cancel, done: make(chan struct{})}
	s.sched = sc
	go func() {
		defer close(sc.done)
		t := time.NewTicker(s.cfg.SchedulerInterval)
		defer t.Stop()
		for {
			s.fireDue(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

func (s *Server) stopScheduler() {
	if s.sched == nil {
		return
	}
	s.sched.cancel()
	<-s.sched.done
}

// fireDue claims every due schedule and starts its run. A claim moves the
// schedule's next_run_at to its next firing after now in one conditional
// UPDATE, so of two replicas or ticks racing for the same firing exactly
// one wins, and however many firings were missed, the schedule fires once.
func (s *Server) fireDue(ctx context.Context) {
	now := s.cfg.Now()
	due, err := s.st.ListDueSchedules(ctx, now)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Error("list due schedules", "error", err)
		}
		return
	}
	for _, d := range due {
		if ctx.Err() != nil {
			return
		}
		var next *time.Time
		cs, perr := cron.Parse(d.Cron, d.Timezone)
		if perr == nil {
			if n := cs.Next(now); !n.IsZero() {
				next = &n
			}
		}
		_, err := s.st.ClaimSchedule(ctx, db.ClaimScheduleParams{ID: d.ID, Due: *d.NextRunAt, Next: next, Now: now})
		if store.IsNotFound(err) {
			continue // claimed elsewhere, or changed since it was listed
		}
		if err != nil {
			s.log.Error("claim schedule", "schedule", d.ID, "error", err)
			continue
		}
		if perr != nil {
			s.skipSchedule(ctx, d.ID, d.OrgID, "", "the cron expression is no longer valid: "+perr.Error(), true)
			continue
		}
		s.fireSchedule(ctx, d.ID, d.OrgID, now)
	}
}

// fireSchedule starts one claimed schedule's run as its owner.
func (s *Server) fireSchedule(ctx context.Context, id, org uuid.UUID, now time.Time) {
	row, err := s.st.GetSchedule(ctx, db.GetScheduleParams{ID: id, OrgID: org})
	if err != nil {
		s.log.Error("load schedule", "schedule", id, "error", err)
		return
	}
	if active(row.LastRunStatus) {
		s.skipSchedule(ctx, id, org, row.Name, fmt.Sprintf("the previous run (%s) was still active", row.LastRunID), false)
		return
	}
	p, reason, err := s.scheduleOwner(ctx, row)
	if err != nil {
		s.log.Error("load schedule owner", "schedule", id, "error", err)
		return
	}
	if reason != "" {
		s.skipSchedule(ctx, id, org, row.Name, reason, true)
		return
	}
	ctx = auth.WithPrincipal(ctx, p)
	h := &handlers{s}
	pr, err := s.st.GetProject(ctx, db.GetProjectParams{ID: row.ProjectID, OrgID: org})
	if err != nil {
		s.log.Error("load schedule project", "schedule", id, "error", err)
		return
	}
	prep, err := h.prepareRun(ctx, org, pr, specOf(row).runInput())
	if err != nil {
		s.skipSchedule(ctx, id, org, row.Name, "the run was not accepted: "+describe(err), true)
		return
	}
	run, err := h.startRun(ctx, p, pr, prep)
	if err != nil {
		s.skipSchedule(ctx, id, org, row.Name, "the run could not start: "+describe(err), true)
		return
	}
	if err := s.st.RecordScheduleRun(ctx, db.RecordScheduleRunParams{ID: id, LastRunID: &run.ID, LastFiredAt: &now}); err != nil {
		s.log.Error("record schedule run", "schedule", id, "error", err)
	}
	s.log.Info("schedule fired", "schedule", id, "name", row.Name, "run", run.ID)
}

// scheduleOwner is the principal a schedule's runs start as. When the
// owner can no longer start runs, it returns why instead.
func (s *Server) scheduleOwner(ctx context.Context, row db.GetScheduleRow) (*auth.Principal, string, error) {
	if row.OwnerID == nil {
		return nil, "its owner's account was deleted; an editor can take it over by saving it", nil
	}
	m, err := s.st.GetMember(ctx, db.GetMemberParams{OrgID: row.OrgID, ID: *row.OwnerID})
	if store.IsNotFound(err) {
		return nil, "its owner is no longer a member of the organisation; an editor can take it over by saving it", nil
	}
	if err != nil {
		return nil, "", err
	}
	role := auth.Role(m.Role)
	if !role.AtLeast(auth.PermRun) {
		return nil, fmt.Sprintf("its owner %s is now a %s and cannot start runs; an editor can take it over by saving it", m.Email, role), nil
	}
	o, err := s.st.GetOrg(ctx, row.OrgID)
	if err != nil {
		return nil, "", err
	}
	return &auth.Principal{
		UserID: m.ID, OrgID: row.OrgID, Email: m.Email, Name: m.Name, OrgName: o.Name, Role: role,
		Via: "schedule:" + row.Name,
	}, "", nil
}

// skipSchedule records why a firing started no run and, when it needs
// someone's attention, writes it to the audit log.
func (s *Server) skipSchedule(ctx context.Context, id, org uuid.UUID, name, reason string, audit bool) {
	s.log.Warn("schedule skipped", "schedule", id, "name", name, "reason", reason)
	if err := s.st.RecordScheduleSkip(ctx, db.RecordScheduleSkipParams{ID: id, LastSkipReason: reason}); err != nil {
		s.log.Error("record schedule skip", "schedule", id, "error", err)
	}
	if audit {
		s.auditSystem(ctx, org, "schedule.skip", name, map[string]any{"schedule": id, "reason": reason})
	}
}

// auditSystem writes an audit entry for something the server did on its
// own, with no signed-in user behind it.
func (s *Server) auditSystem(ctx context.Context, org uuid.UUID, action, subject string, details map[string]any) {
	b, _ := json.Marshal(details)
	err := s.st.InsertAudit(context.WithoutCancel(ctx), db.InsertAuditParams{
		OrgID: org, Actor: "scheduler", Action: action, Subject: subject, Details: b,
	})
	if err != nil {
		s.log.Error("audit write failed", "action", action, "error", err)
	}
}

// describe turns a handler error into one line for a skip reason.
func describe(err error) string {
	var ae *apiError
	if errors.As(err, &ae) {
		if len(ae.details) > 0 {
			return ae.msg + ": " + strings.Join(ae.details, "; ")
		}
		return ae.msg
	}
	return err.Error()
}
