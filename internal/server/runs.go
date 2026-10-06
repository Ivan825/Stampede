package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/runner"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/store/db"
)

// Run statuses.
const (
	statusScheduling = "scheduling"
	statusStarting   = "starting"
	statusRunning    = "running"
	statusStopping   = "stopping"
	statusAnalyzing  = "analyzing"
	statusCompleted  = "completed"
	statusAborted    = "aborted"
	statusFailed     = "failed"
)

type activeRun struct {
	id       uuid.UUID
	org      uuid.UUID
	project  uuid.UUID
	target   uuid.UUID
	scenario uuid.UUID
	hub      *hub
	mu       sync.Mutex
	exec     Execution
	killed   bool
	stopping bool
	done     chan struct{}
}

type runManager struct {
	s      *Server
	mu     sync.Mutex
	active map[uuid.UUID]*activeRun
	wg     sync.WaitGroup

	activeGauge prometheus.Gauge
	finished    *prometheus.CounterVec
}

func newRunManager(s *Server) *runManager {
	return &runManager{
		s:           s,
		active:      map[uuid.UUID]*activeRun{},
		activeGauge: prometheus.NewGauge(prometheus.GaugeOpts{Name: "stampede_runs_active", Help: "Runs currently executing."}),
		finished:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "stampede_runs_finished_total", Help: "Finished runs by final status."}, []string{"status"}),
	}
}

func (m *runManager) metrics() []prometheus.Collector {
	return []prometheus.Collector{m.activeGauge, m.finished}
}

func (m *runManager) get(id uuid.UUID) *activeRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active[id]
}

func (m *runManager) any(pred func(*activeRun) bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.active {
		if pred(a) {
			return true
		}
	}
	return false
}

func (m *runManager) projectHasActiveRun(id uuid.UUID) bool {
	return m.any(func(a *activeRun) bool { return a.project == id })
}
func (m *runManager) targetHasActiveRun(id uuid.UUID) bool {
	return m.any(func(a *activeRun) bool { return a.target == id })
}
func (m *runManager) scenarioHasActiveRun(id uuid.UUID) bool {
	return m.any(func(a *activeRun) bool { return a.scenario == id })
}

// launch registers a run and executes it in the background.
func (m *runManager) launch(r *activeRun, spec ExecSpec, prog *scenario.Program, plan *scenario.Plan) {
	r.hub = newHub()
	r.done = make(chan struct{})
	m.mu.Lock()
	m.active[r.id] = r
	m.mu.Unlock()
	m.activeGauge.Inc()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.execute(r, spec, prog, plan)
	}()
}

func (m *runManager) setStatus(ctx context.Context, r *activeRun, status string) {
	if err := m.s.st.SetRunStatus(ctx, db.SetRunStatusParams{ID: r.id, Status: status}); err != nil {
		m.s.log.Error("set run status", "run", r.id, "error", err)
	}
	m.publishStatus(ctx, r)
}

func (m *runManager) publishStatus(ctx context.Context, r *activeRun) {
	row, err := m.s.st.GetRun(ctx, db.GetRunParams{ID: r.id, OrgID: r.org})
	if err == nil {
		r.hub.publish("status", runOf(row))
	}
}

func (m *runManager) event(ctx context.Context, r *activeRun, ev ExecEvent) {
	d, _ := json.Marshal(ev.Details)
	if ev.Details == nil {
		d = []byte("{}")
	}
	if err := m.s.st.InsertRunEvent(ctx, db.InsertRunEventParams{RunID: r.id, Type: ev.Type, Message: ev.Message, Worker: ev.Worker, Details: d}); err != nil {
		m.s.log.Error("run event", "run", r.id, "error", err)
	}
	r.hub.publish("event", map[string]any{"type": ev.Type, "message": ev.Message, "worker": ev.Worker, "at": time.Now().UTC()})
}

// execute drives one run from start to report. It never panics out: any
// failure ends the run as failed with the error recorded.
func (m *runManager) execute(r *activeRun, spec ExecSpec, prog *scenario.Program, plan *scenario.Plan) {
	ctx := context.Background()
	defer func() {
		m.mu.Lock()
		delete(m.active, r.id)
		m.mu.Unlock()
		m.activeGauge.Dec()
		r.hub.close()
		close(r.done)
	}()
	fail := func(err error) {
		msg := err.Error()
		m.s.log.Error("run failed", "run", r.id, "error", err)
		_ = m.s.st.FinishRun(ctx, db.FinishRunParams{ID: r.id, Status: statusFailed, Error: &msg})
		m.finished.WithLabelValues(statusFailed).Inc()
		m.publishStatus(ctx, r)
	}

	m.setStatus(ctx, r, statusStarting)
	exec, err := m.s.cfg.Executor.Start(ctx, spec)
	if err != nil {
		fail(fmt.Errorf("start: %w", err))
		return
	}
	r.mu.Lock()
	r.exec = exec
	killed := r.killed
	r.mu.Unlock()
	if killed {
		exec.Kill()
	}
	if err := m.s.st.MarkRunStarted(ctx, db.MarkRunStartedParams{ID: r.id, StartedAt: ptr(time.Now())}); err != nil {
		m.s.log.Error("mark started", "run", r.id, "error", err)
	}
	m.publishStatus(ctx, r)

	var bp *runner.BreakpointTracker
	if plan.StopOnFail {
		bp = runner.NewBreakpointTracker(prog, plan)
	}
	var snaps []*metrics.Snapshot
	evDone := make(chan struct{})
	go func() {
		defer close(evDone)
		for ev := range exec.Events() {
			m.event(ctx, r, ev)
		}
	}()
	t0 := time.Now()
	for s := range exec.Snapshots() {
		snaps = append(snaps, s)
		pt := pointOf(s)
		m.storeMetric(ctx, r, t0, s, pt)
		r.hub.publish("point", pt)
		if bp != nil && bp.Observe(s) {
			exec.Stop("breakpoint reached")
		}
	}
	<-evDone

	res, err := exec.Wait(ctx)
	if err != nil {
		fail(err)
		return
	}
	m.setStatus(ctx, r, statusAnalyzing)

	in := report.Input{
		RunID: r.id.String(), Program: prog, Plan: plan, Target: spec.Scenario.Target.BaseURL,
		Started: res.T0, Ended: res.End, StopReason: res.StopReason, PeakVUs: res.PeakVUs,
		Workers: res.Workers, Snapshots: snaps, Phases: res.Phases,
	}
	if bp != nil {
		in.Breakpoint = bp.Result()
	}
	rep := report.Build(in)
	rep.Notes = append(rep.Notes, res.Notes...)
	repJSON, err := json.Marshal(rep)
	if err != nil {
		fail(err)
		return
	}
	if err := m.s.st.SaveReport(ctx, db.SaveReportParams{RunID: r.id, Report: repJSON}); err != nil {
		fail(fmt.Errorf("save report: %w", err))
		return
	}

	r.mu.Lock()
	status := statusCompleted
	if r.killed || res.StopReason == "killed" {
		status = statusAborted
	}
	r.mu.Unlock()
	summary, _ := json.Marshal(gen.RunSummary{
		Requests: ptr(int(rep.Overall.Requests)), ErrorRate: ptr(rep.Overall.ErrorRate), Rps: ptr(rep.Overall.RPS),
		P95: ptr(rep.Overall.Latency.P95), P99: ptr(rep.Overall.Latency.P99),
	})
	verdict, reason := rep.Verdict, res.StopReason
	if err := m.s.st.FinishRun(ctx, db.FinishRunParams{ID: r.id, Status: status, Verdict: &verdict, StopReason: &reason, Summary: summary}); err != nil {
		m.s.log.Error("finish run", "run", r.id, "error", err)
	}
	m.finished.WithLabelValues(status).Inc()
	m.publishStatus(ctx, r)
	m.s.log.Info("run finished", "run", r.id, "status", status, "verdict", verdict, "requests", rep.Overall.Requests)
}

func ptr[T any](v T) *T { return &v }

func pointOf(s *metrics.Snapshot) gen.Point {
	t := s.Totals()
	p := gen.Point{T: float64(s.Interval), Rps: float64(t.Requests), Vus: s.VUs, Planned: s.Planned, Dropped: int(s.Dropped)} //nolint:gosec // counts fit
	if t.Requests > 0 {
		p.ErrorRate = float64(t.Failed) / float64(t.Requests)
		q := t.Latency.Quantiles(0.5, 0.95, 0.99)
		p.P50, p.P95, p.P99 = float64(q[0])/1e6, float64(q[1])/1e6, float64(q[2])/1e6
	}
	var it uint64
	for _, j := range s.Journeys {
		it += j.Completed + j.Failed
	}
	p.Iterations = ptr(int(it)) //nolint:gosec // counts fit
	if s.SchedLag != nil {
		p.SchedLagP99 = ptr(s.SchedLag.QuantileSeconds(0.99))
	}
	return p
}

func (m *runManager) storeMetric(ctx context.Context, r *activeRun, t0 time.Time, s *metrics.Snapshot, p gen.Point) {
	blob, err := json.Marshal(s)
	if err != nil {
		m.s.log.Error("encode snapshot", "run", r.id, "error", err)
		return
	}
	t := s.Totals()
	err = m.s.st.InsertRunMetric(ctx, db.InsertRunMetricParams{
		RunID: r.id, Ts: t0.Add(time.Duration(s.Interval) * time.Second), Interval: int32(s.Interval), //nolint:gosec // intervals are small
		Requests: int64(t.Requests), Failed: int64(t.Failed), Rps: p.Rps, ErrorRate: p.ErrorRate, //nolint:gosec // counts fit
		P50: p.P50, P95: p.P95, P99: p.P99, Vus: int32(s.VUs), Planned: s.Planned, //nolint:gosec // counts fit
		Dropped: int64(s.Dropped), Iterations: int64(*p.Iterations), SchedLag: deref(p.SchedLagP99), Snapshot: blob, //nolint:gosec // counts fit
	})
	if err != nil {
		m.s.log.Error("store metric", "run", r.id, "error", err)
	}
}

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

// stop asks a run to end gracefully.
func (m *runManager) stop(id uuid.UUID) bool {
	r := m.get(id)
	if r == nil {
		return false
	}
	r.mu.Lock()
	exec := r.exec
	r.stopping = true
	r.mu.Unlock()
	if exec != nil {
		exec.Stop("stopped")
	}
	return true
}

// kill stops a run's load immediately. It is safe to call before the
// executor has started: the kill is applied as soon as it does.
func (m *runManager) kill(id uuid.UUID) bool {
	r := m.get(id)
	if r == nil {
		return false
	}
	r.mu.Lock()
	r.killed = true
	exec := r.exec
	r.mu.Unlock()
	if exec != nil {
		exec.Kill()
	}
	return true
}

// recover settles runs left unfinished by a previous server process.
func (m *runManager) recover(ctx context.Context) error {
	ids, err := m.s.st.ListUnfinishedRuns(ctx)
	if err != nil {
		return err
	}
	msg := "the server restarted while this run was in progress"
	for _, id := range ids {
		if err := m.s.st.FinishRun(ctx, db.FinishRunParams{ID: id, Status: statusFailed, Error: &msg}); err != nil {
			return err
		}
	}
	if len(ids) > 0 {
		m.s.log.Warn("marked interrupted runs as failed", "count", len(ids))
	}
	return nil
}

// shutdown stops active runs and waits for their reports.
func (m *runManager) shutdown(ctx context.Context) {
	m.mu.Lock()
	ids := make([]uuid.UUID, 0, len(m.active))
	for id := range m.active {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.stop(id)
	}
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		for _, id := range ids {
			m.kill(id)
		}
	}
}
