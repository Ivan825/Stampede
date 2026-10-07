package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Ivan825/Stampede/internal/ai"
	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
)

func (h *handlers) ListRunEvents(ctx context.Context, req gen.ListRunEventsRequestObject) (gen.ListRunEventsResponseObject, error) {
	r, err := h.run(ctx, req.RunId, auth.PermView)
	if err != nil {
		return nil, err
	}
	rows, err := h.st.ListRunEvents(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	out := gen.ListRunEvents200JSONResponse{}
	for _, e := range rows {
		ev := gen.RunEvent{At: e.At, Type: e.Type, Message: e.Message}
		if e.Worker != "" {
			w := e.Worker
			ev.Worker = &w
		}
		var d map[string]any
		if json.Unmarshal(e.Details, &d) == nil && len(d) > 0 {
			ev.Details = &d
		}
		out = append(out, ev)
	}
	return out, nil
}

// DryRunGateTimeout bounds the dry run a project can require before load.
const DryRunGateTimeout = 2 * time.Minute

// dryRunGate runs each journey once with one user against the target, as
// stampede validate --dry-run and AI generation do, and records the result
// as run events. It returns an error naming the journeys that failed, in
// which case no load must be started.
func (m *runManager) dryRunGate(ctx context.Context, r *activeRun, spec ExecSpec) error {
	m.event(ctx, r, ExecEvent{Type: "dryrun.started", Message: "this project requires a passing dry run: running each journey once with one user before load"})
	dctx, cancel := context.WithTimeout(ctx, DryRunGateTimeout)
	defer cancel()
	r.mu.Lock()
	r.cancelDryRun = cancel
	killed := r.killed || r.stopping
	r.mu.Unlock()
	if killed {
		return errors.New("stopped before the dry run")
	}
	checks, err := ai.DryRunScenario(dctx, spec.Scenario, spec.Scenario.Target.BaseURL, spec.AllowHosts, spec.Env, spec.Secrets, m.s.cfg.DataDir)
	if err != nil {
		if errors.Is(dctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("it took longer than %s", DryRunGateTimeout)
		}
		m.event(ctx, r, ExecEvent{Type: "dryrun.failed", Message: "the dry run could not finish: " + err.Error()})
		return fmt.Errorf("the required dry run could not finish (%v), so no load was started", err)
	}
	var failed []string
	for _, c := range checks {
		if c.OK {
			m.event(ctx, r, ExecEvent{Type: "dryrun.journey", Message: "journey " + c.Journey + " passed its dry run",
				Details: map[string]any{"journey": c.Journey, "ok": true}})
			continue
		}
		problem := c.Problem()
		failed = append(failed, c.Journey+": "+problem)
		m.event(ctx, r, ExecEvent{Type: "dryrun.journey", Message: "journey " + c.Journey + " failed its dry run: " + problem,
			Details: map[string]any{"journey": c.Journey, "ok": false, "problem": problem}})
	}
	if len(failed) > 0 {
		m.event(ctx, r, ExecEvent{Type: "dryrun.failed", Message: fmt.Sprintf("%d of %d journeys failed the dry run; no load was started", len(failed), len(checks))})
		return fmt.Errorf("the required dry run failed, so no load was started: %s", strings.Join(failed, "; "))
	}
	m.event(ctx, r, ExecEvent{Type: "dryrun.passed", Message: fmt.Sprintf("all %d journeys passed the dry run; starting load", len(checks))})
	return nil
}
