package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/runner"
)

// DistributedExecutor runs load on workers connected to a coordinator.
type DistributedExecutor struct {
	Coordinator *coordinator.Coordinator
}

type distExec struct {
	run    *coordinator.Run
	events chan ExecEvent
}

// Start shards the run across connected workers.
func (d *DistributedExecutor) Start(ctx context.Context, spec ExecSpec) (Execution, error) {
	allow := append([]string{spec.TargetHost}, spec.AllowHosts...)
	run, err := d.Coordinator.Start(ctx, coordinator.RunSpec{
		ID: spec.RunID, Scenario: spec.YAML, Env: spec.Env, Secrets: spec.Secrets,
		AllowHosts: allow, Workers: spec.Workers,
	})
	if err != nil {
		return nil, err
	}
	x := &distExec{run: run, events: make(chan ExecEvent, 64)}
	go func() {
		defer close(x.events)
		for ev := range run.Events() {
			e := ExecEvent{Type: string(ev.Type), Message: ev.Message, Worker: ev.WorkerName}
			if len(ev.Reasons) > 0 {
				e.Details = map[string]any{"reasons": ev.Reasons, "interval": ev.Interval}
			}
			x.events <- e
		}
	}()
	return x, nil
}

func (x *distExec) Snapshots() <-chan *metrics.Snapshot { return x.run.Snapshots() }
func (x *distExec) Events() <-chan ExecEvent            { return x.events }
func (x *distExec) Stop(reason string)                  { x.run.StopWithReason(reason) }
func (x *distExec) Kill()                               { x.run.Kill() }

// T0 is when the workers start load, a few seconds after scheduling.
func (x *distExec) T0() time.Time { return x.run.T0() }

func (x *distExec) Wait(ctx context.Context) (*ExecResult, error) {
	res, err := x.run.Wait(ctx)
	if res == nil {
		return nil, err
	}
	return &ExecResult{
		T0: res.T0, End: res.End, StopReason: res.StopReason, Phases: res.Phases,
		PeakVUs: res.PeakVUs, Workers: len(res.Workers),
		// The final result includes snapshots that arrived too late for the
		// live stream, so the report is built from it.
		Snapshots: res.Snapshots,
		Annotate:  func(r *report.Report) { runner.Annotate(r, res) },
	}, err
}

// AutoExecutor uses connected workers when there are any and otherwise
// runs load inside the server, so a single-machine install works with no
// extra setup.
type AutoExecutor struct {
	Local       Executor
	Distributed *DistributedExecutor
}

// Start picks an executor for this run.
func (a *AutoExecutor) Start(ctx context.Context, spec ExecSpec) (Execution, error) {
	if a.Distributed != nil {
		for _, w := range a.Distributed.Coordinator.Workers() {
			if w.Connected && w.CurrentRun == "" {
				return a.Distributed.Start(ctx, spec)
			}
		}
		if spec.Workers > 0 {
			return nil, errors.New("no idle workers are connected")
		}
	}
	return a.Local.Start(ctx, spec)
}

// CoordinatorWorkers adapts a coordinator for GET /workers.
func CoordinatorWorkers(c *coordinator.Coordinator) func() []WorkerInfo {
	return func() []WorkerInfo {
		var out []WorkerInfo
		for _, w := range c.Workers() {
			status := "idle"
			switch {
			case !w.Connected:
				status = "lost"
			case w.Health.Saturated:
				status = "saturated"
			case w.CurrentRun != "":
				status = "busy"
			}
			var protocols, plugins []string
			for _, p := range w.Capacity.Protocols {
				if name, ok := strings.CutPrefix(p, "plugin:"); ok {
					plugins = append(plugins, name)
				} else {
					protocols = append(protocols, p)
				}
			}
			out = append(out, WorkerInfo{
				ID: w.ID, Name: w.Name, Region: w.Region, Version: w.Version, Labels: w.Labels,
				Protocols: protocols, Plugins: plugins,
				CPUs: w.Capacity.CPUs, MemoryBytes: int64(min(w.Capacity.MemoryBytes, 1<<62)), //nolint:gosec // bounded
				Status: strings.ToLower(status), RunID: w.CurrentRun,
				ConnectedAt: w.ConnectedSince, LastSeenAt: w.LastHeartbeat,
			})
		}
		return out
	}
}
