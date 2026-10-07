package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/observe"
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

func isTerminalStatus(s string) bool {
	return s == statusCompleted || s == statusAborted || s == statusFailed
}

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
	killedBy string
	stopping bool
	done     chan struct{}
	// obs is the run's resolved observe block (target metrics, trace links).
	obs *observe.Config
	// faults is the run's fault timeline, injected through an agent.
	faults *runner.FaultPlan
	// link ties the run's span to the request that started it.
	link trace.Link
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
	if err := m.s.st.SetRunOwner(context.Background(), db.SetRunOwnerParams{ID: r.id, OwnerReplica: &m.s.replica.id}); err != nil {
		m.s.log.Error("record run owner", "run", r.id, "error", err)
	}
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
	// One span covers the run from scheduling to its report. It outlives
	// the request that started it, so it is a new trace linked to it.
	ctx, span := tracer.Start(context.Background(), "run",
		trace.WithNewRoot(), trace.WithLinks(r.link), trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("stampede.run.id", r.id.String()),
			attribute.String("stampede.project.id", r.project.String()),
			attribute.String("stampede.scenario", spec.Scenario.Metadata.Name),
			attribute.String("stampede.target", spec.Scenario.Target.BaseURL),
			attribute.String("stampede.load.mode", plan.Mode),
			attribute.Float64("stampede.load.peak", plan.Peak()),
			attribute.Int("stampede.workers.requested", spec.Workers),
		))
	defer span.End()
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
		m.s.notify.runFinished(r, statusFailed, msg, nil)
		span.SetAttributes(attribute.String("stampede.run.status", statusFailed))
		span.SetStatus(codes.Error, msg)
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
	span.AddEvent("load started")
	var faults *runner.FaultRun
	if r.faults != nil {
		t0 := time.Now()
		if s, ok := exec.(interface{ T0() time.Time }); ok {
			t0 = s.T0()
		}
		faults = r.faults.Start(context.WithoutCancel(ctx), t0, r.id.String(), m.s.log.With("run", r.id))
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
	abort := runner.NewAbortWatcher(spec.Scenario.Load.Abort, m.s.cfg.AbortFloor, time.Second)
	t0 := time.Now()
	for s := range exec.Snapshots() {
		snaps = append(snaps, s)
		pt := pointOf(s)
		m.storeMetric(ctx, r, t0, s, pt)
		r.hub.publish("point", pt)
		if bp != nil && bp.Observe(s) {
			exec.Stop("breakpoint reached")
		}
		if reason := abort.Observe(s); reason != "" {
			exec.Stop(reason)
		}
	}
	<-evDone

	res, err := exec.Wait(ctx)
	var faultEvents []report.FaultEvent
	if faults != nil {
		faultEvents = faults.Stop(time.Now())
	}
	if err != nil {
		fail(err)
		return
	}
	// Record how many workers actually ran it (the request may say 0 = all).
	if err := m.s.st.SetRunWorkers(ctx, db.SetRunWorkersParams{ID: r.id, Workers: int32(res.Workers)}); err != nil { //nolint:gosec // small
		m.s.log.Error("set run workers", "run", r.id, "error", err)
	}
	span.AddEvent("load ended", trace.WithAttributes(attribute.String("stampede.stop_reason", res.StopReason), attribute.Int("stampede.workers", res.Workers)))

	if res.Snapshots != nil {
		snaps = res.Snapshots
	}
	in := report.Input{
		RunID: r.id.String(), Program: prog, Plan: plan, Target: spec.Scenario.Target.BaseURL,
		Started: res.T0, Ended: res.End, StopReason: res.StopReason, PeakVUs: res.PeakVUs,
		Workers: res.Workers, Snapshots: snaps, Phases: res.Phases,
	}
	if bp != nil {
		in.Breakpoint = bp.Result()
		r.mu.Lock()
		halted := r.killed || r.stopping
		r.mu.Unlock()
		if in.Breakpoint.Found && !halted && res.StopReason == "breakpoint reached" {
			if err := runner.Refine(ctx, in.Breakpoint, plan.Mode, runner.RefineRounds, func(ctx context.Context, level float64) (bool, []string, error) {
				return m.confirm(ctx, r, spec, plan, level)
			}); err != nil {
				m.s.log.Warn("breakpoint refinement stopped", "run", r.id, "error", err)
			}
		}
	}
	m.setStatus(ctx, r, statusAnalyzing)
	rep := report.Build(in)
	rep.Notes = append(rep.Notes, res.Notes...)
	rep.Faults = faultEvents
	if res.Annotate != nil {
		res.Annotate(rep)
	}
	if !r.obs.Empty() {
		octx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		r.obs.Apply(octx, rep, time.Second)
		cancel()
	}
	repJSON, err := json.Marshal(rep)
	if err != nil {
		fail(err)
		return
	}
	// The load is over and the report exists only in memory: ride out a
	// database outage rather than lose it.
	if err := m.retryDB(ctx, r, "save report", func() error {
		return m.s.st.SaveReport(ctx, db.SaveReportParams{RunID: r.id, Report: repJSON})
	}); err != nil {
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
	if err := m.retryDB(ctx, r, "finish run", func() error {
		return m.s.st.FinishRun(ctx, db.FinishRunParams{ID: r.id, Status: status, Verdict: &verdict, StopReason: &reason, Summary: summary})
	}); err != nil {
		m.s.log.Error("finish run", "run", r.id, "error", err)
	}
	m.finished.WithLabelValues(status).Inc()
	m.publishStatus(ctx, r)
	m.s.log.Info("run finished", "run", r.id, "status", status, "verdict", verdict, "requests", rep.Overall.Requests)
	m.s.notify.runFinished(r, status, "", rep)
	span.SetAttributes(
		attribute.String("stampede.run.status", status),
		attribute.String("stampede.run.verdict", verdict),
		attribute.Int64("stampede.requests", int64(rep.Overall.Requests)), //nolint:gosec // counts fit
		attribute.Float64("stampede.error_rate", rep.Overall.ErrorRate),
		attribute.Float64("stampede.p95_seconds", rep.Overall.Latency.P95),
	)
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
// executor has started: the kill is applied as soon as it does. by names
// who pulled the kill switch, for notifications.
func (m *runManager) kill(id uuid.UUID, by string) bool {
	r := m.get(id)
	if r == nil {
		return false
	}
	r.mu.Lock()
	if !r.killed {
		r.killedBy = by
	}
	r.killed = true
	exec := r.exec
	r.mu.Unlock()
	if exec != nil {
		exec.Kill()
	}
	return true
}

// killOrg kills every run of an organisation running on this replica.
func (m *runManager) killOrg(org uuid.UUID, by string) {
	m.mu.Lock()
	var ids []uuid.UUID
	for id, r := range m.active {
		if r.org == org {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.kill(id, by)
	}
}

// recover settles runs left unfinished by a previous server process.
func (m *runManager) recover(ctx context.Context) error {
	// Only runs whose replica is gone: other live replicas keep theirs.
	ids, err := m.s.st.ListOrphanedRuns(ctx, m.s.cfg.Now().Add(-m.s.cfg.ReplicaStale))
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
			m.kill(id, "server shutdown")
		}
	}
}

var errStopped = errors.New("the run was stopped")

// confirm runs one breakpoint confirmation hold at level on the run's
// executor (in-process or on workers). Stop and kill reach it like the
// main run.
func (m *runManager) confirm(ctx context.Context, r *activeRun, spec ExecSpec, plan *scenario.Plan, level float64) (bool, []string, error) {
	s, p, err := runner.ConfirmScenario(spec.Scenario, plan, level)
	if err != nil {
		return false, nil, err
	}
	prog, err := scenario.Compile(s)
	if err != nil {
		return false, nil, err
	}
	y, err := s.Marshal()
	if err != nil {
		return false, nil, err
	}
	cs := spec
	cs.Scenario, cs.YAML = s, y
	m.event(ctx, r, ExecEvent{Type: "breakpoint.confirm", Message: fmt.Sprintf("confirming the breakpoint at %s%s", strconv.FormatFloat(level, 'f', -1, 64), report.Unit(plan.Mode))})
	exec, err := m.s.cfg.Executor.Start(ctx, cs)
	if err != nil {
		return false, nil, err
	}
	r.mu.Lock()
	r.exec = exec
	stop := r.killed || r.stopping
	r.mu.Unlock()
	if stop {
		exec.Kill()
	}
	go func() {
		for range exec.Events() {
		}
	}()
	tr := runner.NewBreakpointTracker(prog, p)
	for snap := range exec.Snapshots() {
		tr.Observe(snap)
	}
	if _, err := exec.Wait(ctx); err != nil {
		return false, nil, err
	}
	r.mu.Lock()
	stop = stop || r.killed || r.stopping
	r.mu.Unlock()
	if stop {
		return false, nil, errStopped
	}
	out := tr.Result()
	return !out.Found, out.FailedOn, nil
}

// retryDB runs a database write until it succeeds, backing off from half
// a second to five, for up to the configured DBRetryFor (two minutes by
// default), so a short database outage does not lose a finished run.
func (m *runManager) retryDB(ctx context.Context, r *activeRun, what string, fn func() error) error {
	limit := m.s.cfg.DBRetryFor
	if limit <= 0 {
		limit = 2 * time.Minute
	}
	deadline := time.Now().Add(limit)
	wait := 500 * time.Millisecond
	for {
		err := fn()
		if err == nil || time.Now().After(deadline) || ctx.Err() != nil {
			return err
		}
		m.s.log.Warn("database write failed; retrying", "run", r.id, "write", what, "in", wait, "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait = min(wait*2, 5*time.Second)
	}
}
