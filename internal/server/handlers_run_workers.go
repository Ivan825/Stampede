package server

import (
	"context"
	"sort"
	"time"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
)

// WorkerHealth is one load generator's latest self-monitoring during a run.
type WorkerHealth struct {
	ID, Name, Region string
	// Connected is false once the worker stopped sending heartbeats.
	Connected   bool
	Saturated   bool
	Reasons     []string
	CPUPercent  float64
	SchedLagP99 time.Duration
	GCPauseP99  time.Duration
	Dropped     uint64
	// LastHeartbeat is zero before the first sample.
	LastHeartbeat time.Time
}

// healthReporter is an Execution that can report its load generators'
// health while it runs.
type healthReporter interface {
	WorkerHealth() []WorkerHealth
}

// WorkerHealth reports the server itself, which generates the load.
func (x *localExec) WorkerHealth() []WorkerHealth {
	h, at := x.self.Latest()
	return []WorkerHealth{{
		ID: "local", Name: "this server", Connected: true,
		Saturated: h.Saturated, Reasons: h.Reasons, CPUPercent: h.CPUPercent,
		SchedLagP99: h.SchedLagP99, GCPauseP99: h.GCPauseP99, Dropped: h.Dropped, LastHeartbeat: at,
	}}
}

// WorkerHealth reports the coordinator's view of the run's workers.
func (x *distExec) WorkerHealth() []WorkerHealth {
	if x.coord == nil {
		return nil
	}
	var out []WorkerHealth
	for _, w := range x.coord.Workers() {
		if w.CurrentRun != x.run.ID() {
			continue
		}
		out = append(out, WorkerHealth{
			ID: w.ID, Name: w.Name, Region: w.Region, Connected: w.Connected,
			Saturated: w.Health.Saturated, Reasons: w.Health.Reasons, CPUPercent: w.Health.CPUPercent,
			SchedLagP99: w.Health.SchedLagP99, GCPauseP99: w.Health.GCPauseP99, Dropped: w.Health.Dropped,
			LastHeartbeat: w.LastHeartbeat,
		})
	}
	return out
}

func runWorkerHealthOf(w WorkerHealth) gen.RunWorkerHealth {
	status := gen.RunWorkerHealthy
	switch {
	case !w.Connected:
		status = gen.RunWorkerLost
	case w.Saturated:
		status = gen.RunWorkerSaturated
	}
	out := gen.RunWorkerHealth{
		Id: w.ID, Name: w.Name, Status: status, Saturated: w.Saturated,
		CpuPercent: w.CPUPercent, SchedLagP99: w.SchedLagP99.Seconds(),
	}
	if w.Region != "" {
		out.Region = ptr(w.Region)
	}
	if len(w.Reasons) > 0 {
		out.Reasons = ptr(append([]string(nil), w.Reasons...))
	}
	if w.GCPauseP99 > 0 {
		out.GcPauseP99 = ptr(w.GCPauseP99.Seconds())
	}
	if w.Dropped > 0 {
		out.Dropped = ptr(int64(min(w.Dropped, 1<<62))) //nolint:gosec // bounded
	}
	if !w.LastHeartbeat.IsZero() {
		out.LastHeartbeatAt = ptr(w.LastHeartbeat.UTC())
	}
	return out
}

func (h *handlers) ListRunWorkers(ctx context.Context, req gen.ListRunWorkersRequestObject) (gen.ListRunWorkersResponseObject, error) {
	r, err := h.run(ctx, req.RunId, auth.PermView)
	if err != nil {
		return nil, err
	}
	out := gen.RunWorkers{Workers: []gen.RunWorkerHealth{}}
	a := h.runs.get(r.ID)
	if a == nil {
		return gen.ListRunWorkers200JSONResponse(out), nil
	}
	out.Live = true
	a.mu.Lock()
	exec := a.exec
	a.mu.Unlock()
	hr, ok := exec.(healthReporter)
	if !ok {
		// Scheduling, or an executor that does not report health.
		return gen.ListRunWorkers200JSONResponse(out), nil
	}
	ws := hr.WorkerHealth()
	sort.SliceStable(ws, func(i, j int) bool { return ws[i].Name < ws[j].Name })
	for _, w := range ws {
		out.Workers = append(out.Workers, runWorkerHealthOf(w))
	}
	return gen.ListRunWorkers200JSONResponse(out), nil
}
