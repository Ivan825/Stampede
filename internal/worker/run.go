package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	workerv1 "github.com/Ivan825/Stampede/gen/stampede/worker/v1"
	"github.com/Ivan825/Stampede/internal/engine"
	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/wire"
)

// maxOutbox bounds unacknowledged snapshots kept for resending: an hour
// of one-second intervals. Older ones are dropped first.
const maxOutbox = 3600

// StopDeadMan is the stop reason when the dead man's switch fires.
const StopDeadMan = "lost contact with server"

// activeRun is the run this worker is executing. It outlives connections:
// a reconnect resends its unacknowledged snapshots.
type activeRun struct {
	id     string
	eng    *engine.Engine
	t0     time.Time // in time.Now's clock
	start  time.Time // t0, or later for a takeover
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu         sync.Mutex
	stopReason string
	outbox     []*workerv1.Snapshot
	lastSeq    uint64
	finished   *workerv1.RunFinished
}

// startRun handles StartRun: it compiles the scenario and answers with
// RunAccepted or RunFailed. Load starts at the synchronised T0.
func (w *Worker) startRun(ctx context.Context, s *session, sr *workerv1.StartRun) {
	id := sr.GetRunId()
	accepted := &workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_RunAccepted{RunAccepted: &workerv1.RunAccepted{RunId: id}}}
	w.mu.Lock()
	cur := w.run
	w.mu.Unlock()
	if cur != nil {
		cur.mu.Lock()
		busy := cur.finished == nil
		cur.mu.Unlock()
		switch {
		case cur.id == id:
			// A repeated StartRun, for example after a reconnect.
			s.send(accepted)
			return
		case busy:
			s.send(runFailed(id, fmt.Errorf("worker is busy with run %s", cur.id)))
			return
		}
	}

	if g := w.cfg.Gate; g != nil && !g.acquire(id) {
		s.send(runFailed(id, fmt.Errorf("worker is busy with run %s from another server", g.busy())))
		return
	}
	r, err := w.prepare(ctx, sr)
	if err != nil {
		w.cfg.Gate.release(id)
		w.log.Error("cannot start run", "run", id, "error", err)
		s.send(runFailed(id, err))
		return
	}
	w.mu.Lock()
	w.run = r
	w.mu.Unlock()
	s.send(accepted)
	w.log.Info("run accepted", "run", id, "share", fmt.Sprintf("[%.4f, %.4f)", sr.GetShareLo(), sr.GetShareHi()),
		"index", sr.GetWorkerIndex(), "of", sr.GetWorkerCount(), "starts_in", time.Until(r.start).Round(time.Millisecond))
	if w.cfg.OnRunStart != nil {
		w.cfg.OnRunStart(id, r.t0)
	}
	go w.execute(r)
	go w.deadMan(r)
}

// prepare builds the engine for a StartRun.
func (w *Worker) prepare(ctx context.Context, sr *workerv1.StartRun) (*activeRun, error) {
	sc, err := scenario.Parse(sr.GetScenario())
	if err != nil {
		return nil, err
	}
	prog, err := scenario.Compile(sc)
	if err != nil {
		return nil, err
	}
	plan, err := sc.Load.Plan()
	if err != nil {
		return nil, err
	}
	// T0 arrives in this worker's clock; the engine schedules with
	// time.Now, so translate through the difference between the two.
	t0 := time.Now().Add(time.Unix(0, sr.GetT0UnixNano()).Sub(w.cfg.Clock()))

	// The server decides which public hosts are allowed (it verified
	// them); private hosts are always allowed. An empty target name never
	// matches a request host, so only the server's list applies.
	policy := safety.NewHostPolicy("", sr.GetAllowHosts())

	r := &activeRun{id: sr.GetRunId(), t0: t0, start: t0.Add(time.Duration(sr.GetResumeNs())), done: make(chan struct{})}
	// The run belongs to the worker, not to the connection that started
	// it: it keeps going across reconnects.
	r.ctx, r.cancel = context.WithCancel(ctx)
	r.eng, err = engine.New(engine.Options{
		Program: prog, Plan: plan, RunID: sr.GetRunId(),
		Env: sr.GetEnv(), Secrets: sr.GetSecrets(),
		ShareLo: sr.GetShareLo(), ShareHi: sr.GetShareHi(),
		WorkerIndex: int(sr.GetWorkerIndex()), WorkerCount: int(sr.GetWorkerCount()),
		T0: t0, Interval: time.Duration(sr.GetIntervalNs()),
		Resume: time.Duration(sr.GetResumeNs()), Attempt: int(sr.GetAttempt()),
		OnSnapshot: func(s *metrics.Snapshot) { w.onSnapshot(r, s) },
		AllowHost:  policy.Allow, HTTP: w.cfg.HTTP, Logger: w.log, PluginDir: w.cfg.PluginDir,
	})
	if err != nil {
		r.cancel()
		return nil, err
	}
	return r, nil
}

// onSnapshot runs on the engine's flush goroutine. It must not block, so
// it only queues: the session writer does the network I/O.
func (w *Worker) onSnapshot(r *activeRun, s *metrics.Snapshot) {
	var lag time.Duration
	if s.SchedLag != nil {
		lag = time.Duration(s.SchedLag.Quantile(0.99)) * time.Microsecond
	}
	h := w.mon.Observe(lag, s.Dropped)
	p, err := wire.SnapshotToProto(r.id, s, &h)
	if err != nil {
		w.log.Error("cannot encode snapshot", "run", r.id, "interval", s.Interval, "error", err)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.outbox) >= maxOutbox {
		r.outbox = r.outbox[1:]
	}
	r.outbox = append(r.outbox, p)
	r.lastSeq = s.Seq
	w.sendNow(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_Snapshot{Snapshot: p}})
}

// execute runs the engine and reports the end of the run.
func (w *Worker) execute(r *activeRun) {
	defer w.cfg.Gate.release(r.id)
	defer close(r.done)
	defer r.cancel()
	res, err := r.eng.Run(r.ctx)
	if err != nil && res == nil {
		// Stopped before T0: no load was generated.
		r.mu.Lock()
		reason := r.stopReason
		r.mu.Unlock()
		res = &engine.Result{T0: r.t0, End: time.Now(), StopReason: reason}
		if r.ctx.Err() == nil {
			w.log.Error("run failed", "run", r.id, "error", err)
			w.sendNow(runFailed(r.id, err))
			w.finishLocal(r, nil)
			return
		}
	}
	// Report times in the worker's clock, like every other time it sends.
	shift := time.Until(w.cfg.Clock())
	fin := &workerv1.RunFinished{
		RunId: r.id, StopReason: res.StopReason, PeakVus: uint32(max(res.PeakVUs, 0)),
		T0UnixNano: res.T0.Add(shift).UnixNano(), EndUnixNano: res.End.Add(shift).UnixNano(),
	}
	if fin.Phases, err = wire.PhasesToProto(res.Phases); err != nil {
		w.log.Error("cannot encode phase histograms", "run", r.id, "error", err)
	}
	w.finishLocal(r, fin)
	w.log.Info("run finished", "run", r.id, "reason", res.StopReason, "peak_vus", res.PeakVUs)
}

// finishLocal records the end of the run and sends RunFinished; it is
// resent on reconnect until the server acknowledges it.
func (w *Worker) finishLocal(r *activeRun, fin *workerv1.RunFinished) {
	w.mon.Idle()
	r.mu.Lock()
	defer r.mu.Unlock()
	if fin == nil {
		// RunFailed was sent; nothing more to deliver.
		r.finished = &workerv1.RunFinished{RunId: r.id}
		w.mu.Lock()
		if w.run == r {
			w.run = nil
		}
		w.mu.Unlock()
		return
	}
	fin.LastSeq = r.lastSeq
	r.finished = fin
	w.sendNow(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_RunFinished{RunFinished: fin}})
}

// stopRun handles StopRun.
func (w *Worker) stopRun(sr *workerv1.StopRun) {
	w.mu.Lock()
	r := w.run
	w.mu.Unlock()
	if r == nil || r.id != sr.GetRunId() {
		return
	}
	w.stop(r, sr.GetKill(), sr.GetReason())
}

func (w *Worker) stop(r *activeRun, kill bool, reason string) {
	if w.cfg.OnStop != nil {
		w.cfg.OnStop(r.id, kill, reason)
	}
	if reason == "" {
		reason = engine.StopRequested
	}
	if kill {
		reason = engine.StopKilled
	}
	r.mu.Lock()
	if r.stopReason == "" {
		r.stopReason = reason
	}
	r.mu.Unlock()
	if kill {
		r.eng.Kill()
		// Also ends the wait for T0 if the run has not started yet.
		r.cancel()
		return
	}
	r.eng.Stop(reason)
	if time.Now().Before(r.start) {
		r.cancel()
	}
}

// ack forgets snapshots the server has stored.
func (w *Worker) ack(a *workerv1.Ack) {
	w.mu.Lock()
	r := w.run
	w.mu.Unlock()
	if r == nil || r.id != a.GetRunId() {
		return
	}
	r.mu.Lock()
	i := 0
	for i < len(r.outbox) && r.outbox[i].GetSeq() <= a.GetSeq() {
		i++
	}
	r.outbox = r.outbox[i:]
	done := a.GetFinished() && r.finished != nil
	r.mu.Unlock()
	if done {
		w.mu.Lock()
		if w.run == r {
			w.run = nil
		}
		w.mu.Unlock()
	}
}

// deadMan stops the run's load when the server has been silent for the
// dead man's switch timeout. Load never runs unsupervised for long: a
// partitioned worker cannot be stopped by anyone else.
func (w *Worker) deadMan(r *activeRun) {
	t := time.NewTicker(min(w.cfg.DeadManTimeout/10, 250*time.Millisecond))
	defer t.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-t.C:
			silent := time.Since(time.Unix(0, w.lastContact.Load()))
			if silent > w.cfg.DeadManTimeout {
				w.log.Error("no contact with server; stopping load (dead man's switch)", "run", r.id, "silent_for", silent.Round(time.Millisecond))
				w.stop(r, false, StopDeadMan)
				return
			}
		}
	}
}

// Gate lets the Workers of one process (one per server replica, see
// Config.Gate) share the machine: only one of them runs load at a time.
// A nil Gate allows everything.
type Gate struct {
	mu  sync.Mutex
	run string
}

func (g *Gate) acquire(run string) bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.run != "" && g.run != run {
		return false
	}
	g.run = run
	return true
}

func (g *Gate) release(run string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.run == run {
		g.run = ""
	}
}

// busy returns the run holding the gate, if any.
func (g *Gate) busy() string {
	if g == nil {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.run
}
