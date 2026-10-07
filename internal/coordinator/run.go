package coordinator

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	workerv1 "github.com/Ivan825/Stampede/gen/stampede/worker/v1"
	"github.com/Ivan825/Stampede/internal/engine"
	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/wire"
)

// RunSpec describes a distributed run.
type RunSpec struct {
	ID       string
	Scenario []byte // scenario YAML
	Env      map[string]string
	Secrets  map[string]string
	// AllowHosts lists the public hosts workers may send requests to,
	// normally the verified target host. Private hosts are always allowed.
	AllowHosts []string
	// Workers is how many workers to use (0 = every idle connected one).
	Workers int
	// Regions optionally splits load by worker region, for example
	// {"mumbai": 0.5, "frankfurt": 0.5}. Fractions are normalised.
	Regions map[string]float64
	// StartDelay is the lead time before the synchronised T0 (default 3s).
	StartDelay time.Duration
}

// EventType names a run event.
type EventType string

// Run events.
const (
	// EventWorkerJoined: the worker accepted its share of the run.
	EventWorkerJoined EventType = "worker-joined"
	// EventWorkerStarted: the worker's first snapshot arrived.
	EventWorkerStarted EventType = "worker-started"
	// EventWorkerSaturated: the worker reported itself saturated for an
	// interval; Reasons says why.
	EventWorkerSaturated EventType = "worker-saturated"
	// EventWorkerLost: three heartbeats missed (or the worker never
	// finished after being stopped). Its data up to the loss is kept.
	EventWorkerLost EventType = "worker-lost"
	// EventDegraded follows a loss: the lost share is not reassigned and
	// the run continues with the remaining workers.
	EventDegraded EventType = "degraded"
	// EventTakeover follows a loss when a spare worker takes over the lost
	// share from a later interval; until then the run is short of it.
	EventTakeover EventType = "takeover"
	// EventWorkerFinished: the worker's part of the run ended.
	EventWorkerFinished EventType = "worker-finished"
	// EventWorkerFailed: the worker reported an error and stopped.
	EventWorkerFailed EventType = "worker-failed"
	// EventIntervalIncomplete: an interval was emitted without some
	// workers' data (listed in Workers); late data still counts in the
	// final result.
	EventIntervalIncomplete EventType = "interval-incomplete"
)

// RunEvent is something that happened during a run.
type RunEvent struct {
	Time       time.Time `json:"time"`
	Type       EventType `json:"type"`
	Worker     string    `json:"worker,omitempty"`
	WorkerName string    `json:"workerName,omitempty"`
	Interval   int64     `json:"interval"`
	Message    string    `json:"message"`
	Reasons    []string  `json:"reasons,omitempty"`
	Workers    []string  `json:"workers,omitempty"`
}

// Window is an inclusive range of intervals.
type Window struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// Worker states in a Result.
const (
	WorkerFinished = "finished"
	WorkerFailed   = "failed"
	WorkerLost     = "lost"
)

// WorkerSummary is one worker's part in a run.
type WorkerSummary struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Region      string        `json:"region,omitempty"`
	Index       int           `json:"index"`
	ShareLo     float64       `json:"shareLo"`
	ShareHi     float64       `json:"shareHi"`
	ClockOffset time.Duration `json:"clockOffset"`
	ClockRTT    time.Duration `json:"clockRTT"`
	State       string        `json:"state"`
	StopReason  string        `json:"stopReason,omitempty"`
	Error       string        `json:"error,omitempty"`
	PeakVUs     int           `json:"peakVUs"`
	Requests    uint64        `json:"requests"`
	// Iterations counts iterations started.
	Iterations uint64 `json:"iterations"`
	Snapshots  int    `json:"snapshots"`
	// Saturated lists the intervals in which the worker reported itself
	// saturated, and SaturationReasons the distinct reasons.
	Saturated         []Window `json:"saturated,omitempty"`
	SaturationReasons []string `json:"saturationReasons,omitempty"`
	// Lost is the window from the loss until the share was taken over, or
	// to the end of the run.
	Lost *Window `json:"lost,omitempty"`
	// Replaces names the lost worker whose share this one took over, from
	// interval JoinedAt.
	Replaces string `json:"replaces,omitempty"`
	JoinedAt int64  `json:"joinedAt,omitempty"`
	// Missing lists intervals emitted live without this worker's data.
	Missing []int64 `json:"missing,omitempty"`
}

// Result is the outcome of a distributed run.
type Result struct {
	RunID      string
	T0         time.Time
	End        time.Time
	Interval   time.Duration
	StopReason string
	// Phases are run-wide per-step phase histograms merged across workers.
	Phases map[int]*[metrics.NumPhases]*metrics.Histogram
	// PeakVUs sums the workers' peaks: exact for closed-model plans,
	// whose shares peak together.
	PeakVUs int
	// Snapshots are the merged intervals in order, including data that
	// arrived too late for the live stream.
	Snapshots []*metrics.Snapshot
	Workers   []WorkerSummary
	// Degraded is true when a worker was lost or failed mid-run.
	Degraded bool
	// LateSnapshots counts worker snapshots that missed the live stream.
	LateSnapshots uint64
}

type memberState int

const (
	memberPending memberState = iota
	memberAccepted
	memberFinished
	memberFailed
	memberLost
)

func (s memberState) terminal() bool { return s >= memberFinished }

// member is a worker's participation in a run. Only the run's goroutine
// touches the mutable fields (and Start, before that goroutine exists).
type member struct {
	w           *workerConn
	cand        candidate
	sh          share
	offset, rtt time.Duration

	state        memberState
	started      bool
	snapshots    int
	lastInterval int64
	requests     uint64
	iterations   uint64
	peakVUs      int
	end          time.Time
	stopReason   string
	errMsg       string
	saturated    []int64
	satReasons   map[string]bool
	missing      []int64
	lostAt       int64
	killSentAt   time.Time

	// dataIndex partitions unique test data: the original member's index,
	// kept by whoever takes over its share.
	dataIndex int
	// joinAt is the first interval a takeover reports (0 otherwise).
	joinAt int64
	// attempt counts takeovers of this share; replaces and replacedBy
	// link a lost member and its successor (-1 when none).
	attempt              int
	replaces, replacedBy int
	resume               time.Duration
}

type inMsg struct {
	member   int
	msg      *workerv1.WorkerMessage
	at       time.Time
	reattach bool
	// joined reports a takeover's clock synchronisation.
	joined *joinResult
}

type joinResult struct {
	offset, rtt time.Duration
	err         error
}

// Run is a distributed run in progress.
type Run struct {
	c    *Coordinator
	spec RunSpec
	plan *scenario.Plan
	t0   time.Time
	iv   time.Duration
	// members grows when a spare takes over a lost share; mmu guards the
	// slice for Stop and Kill, which run outside the run's goroutine.
	mmu     sync.RWMutex
	members []*member
	// dataCount is the number of original members.
	dataCount int
	// noTakeover explains why lost shares cannot be handed over.
	noTakeover string
	merger     *merger
	phases     map[int]*[metrics.NumPhases]*metrics.Histogram

	inbox  chan inMsg
	snaps  chan *metrics.Snapshot
	events chan RunEvent
	done   chan struct{}
	result *Result
	err    error

	hardDeadline time.Time

	ctl        sync.Mutex
	stopReason string
	stopAt     time.Time
	killed     bool
}

// Start splits spec across idle workers, synchronises their clocks and
// starts them at a common T0. It returns once every worker has accepted
// its share, or an error (and no load) when there is not enough capacity
// or a worker refuses. ctx bounds only this setup; the run itself ends
// when the plan completes or on Stop or Kill.
func (c *Coordinator) Start(ctx context.Context, spec RunSpec) (*Run, error) {
	if spec.ID == "" {
		return nil, errors.New("run id is required")
	}
	sc, err := scenario.Parse(spec.Scenario)
	if err != nil {
		return nil, err
	}
	if _, err := scenario.Compile(sc); err != nil {
		return nil, err
	}
	plan, err := sc.Load.Plan()
	if err != nil {
		return nil, err
	}
	if spec.StartDelay <= 0 {
		spec.StartDelay = 3 * time.Second
	}

	r, err := c.reserve(spec, plan)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			r.abort()
		}
	}()

	// Measure every worker's clock offset right before the start.
	var wg sync.WaitGroup
	errs := make([]error, len(r.members))
	for i, m := range r.members {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.offset, m.rtt, errs[i] = m.w.syncClock(ctx, c.cfg.ClockSamples)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("clock synchronisation: %w", err)
	}

	r.t0 = time.Now().Add(spec.StartDelay)
	grace := c.cfg.MergeGrace
	if grace <= 0 {
		grace = 2 * r.iv
	}
	r.merger = newMerger(len(r.members), r.t0, r.iv, grace, plannedAt(plan, r.iv))
	r.hardDeadline = r.t0.Add(plan.TotalDuration() + plan.GracefulStop + 15*time.Second)
	n := uint32(len(r.members))
	for i, m := range r.members {
		start := &workerv1.StartRun{
			RunId: spec.ID, Scenario: spec.Scenario, Env: spec.Env, Secrets: spec.Secrets,
			ShareLo: m.sh.lo, ShareHi: m.sh.hi, WorkerIndex: uint32(i), WorkerCount: n,
			// T0 in the worker's own clock.
			T0UnixNano: r.t0.Add(m.offset).UnixNano(),
			AllowHosts: spec.AllowHosts, IntervalNs: int64(r.iv),
		}
		if !m.w.send(&workerv1.ServerMessage{Msg: &workerv1.ServerMessage_StartRun{StartRun: start}}) {
			return nil, errNotConnected(m.cand.name)
		}
	}

	// Every worker must accept before T0, leaving a margin for the
	// message to arrive; otherwise nobody starts.
	accept := time.NewTimer(spec.StartDelay * 3 / 4)
	defer accept.Stop()
	for !r.allAccepted() {
		select {
		case in := <-r.inbox:
			r.handle(in)
			for _, m := range r.members {
				if m.state == memberFailed {
					return nil, fmt.Errorf("worker %s refused the run: %s", m.cand.name, m.errMsg)
				}
			}
		case <-accept.C:
			var slow []string
			for _, m := range r.members {
				if m.state == memberPending {
					slow = append(slow, m.cand.name)
				}
			}
			return nil, fmt.Errorf("workers did not accept the run before its start: %s", strings.Join(slow, ", "))
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ok = true
	c.log.Info("run started", "run", spec.ID, "workers", len(r.members), "t0", r.t0)
	go r.loop()
	return r, nil
}

// spare finds an idle worker that speaks protocol v1.4 (resumable
// starts) and attaches it to r as member idx. With sameRegion only a
// worker in region qualifies; otherwise one there is preferred.
func (c *Coordinator) spare(r *Run, idx int, region string, sameRegion bool) (*workerConn, candidate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	stale := 3 * c.cfg.HeartbeatInterval
	var best *workerConn
	var bestCand candidate
	for _, w := range c.workers {
		w.mu.Lock()
		ok := w.stream != nil && w.run == nil && !w.busyElsewhere && now.Sub(w.seen()) < stale && w.minor >= 4
		cand := candidate{id: w.id, name: w.name, region: w.region, cpus: float64(max(w.capacity.CPUs, 1)), maxVUs: w.capacity.MaxVUs}
		w.mu.Unlock()
		if !ok || (sameRegion && cand.region != region) {
			continue
		}
		in, bestIn := cand.region == region, best != nil && bestCand.region == region
		if best == nil || (in && !bestIn) || (in == bestIn && cand.cpus > bestCand.cpus) {
			best, bestCand = w, cand
		}
	}
	if best != nil {
		best.mu.Lock()
		best.run, best.member = r, idx
		best.mu.Unlock()
	}
	return best, bestCand
}

// reserve chooses the workers and marks them busy with the new run.
func (c *Coordinator) reserve(spec RunSpec, plan *scenario.Plan) (*Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.runs[spec.ID]; dup {
		return nil, fmt.Errorf("run %s is already running", spec.ID)
	}
	now := time.Now()
	stale := 3 * c.cfg.HeartbeatInterval
	var avail []candidate
	byID := map[string]*workerConn{}
	for _, w := range c.workers {
		w.mu.Lock()
		if w.stream != nil && w.run == nil && !w.busyElsewhere && now.Sub(w.seen()) < stale {
			avail = append(avail, candidate{
				id: w.id, name: w.name, region: w.region,
				cpus: float64(max(w.capacity.CPUs, 1)), maxVUs: w.capacity.MaxVUs,
			})
			byID[w.id] = w
		}
		w.mu.Unlock()
	}
	sel, err := selectWorkers(avail, spec.Workers, spec.Regions, plan)
	if err != nil {
		return nil, err
	}

	iv := time.Second
	intervals := int((plan.TotalDuration()+plan.GracefulStop)/iv) + 32
	r := &Run{
		c: c, spec: spec, plan: plan, iv: iv,
		phases: map[int]*[metrics.NumPhases]*metrics.Histogram{},
		inbox:  make(chan inMsg, 4096),
		snaps:  make(chan *metrics.Snapshot, intervals),
		events: make(chan RunEvent, 4096),
		done:   make(chan struct{}),
	}
	for i, cand := range sel.workers {
		w := byID[cand.id]
		w.mu.Lock()
		w.run, w.member = r, i
		w.mu.Unlock()
		r.members = append(r.members, &member{
			w: w, cand: cand, sh: sel.shares[i], lastInterval: -1, lostAt: -1, satReasons: map[string]bool{},
			dataIndex: i, replaces: -1, replacedBy: -1,
		})
	}
	r.dataCount = len(r.members)
	r.noTakeover = takeoverBlocker(spec, plan, c.cfg.NoTakeover)
	c.runs[spec.ID] = r
	return r, nil
}

// takeoverBlocker says why a lost worker's share cannot be handed to a
// spare in this run, or "" when it can.
func takeoverBlocker(spec RunSpec, plan *scenario.Plan, disabled bool) string {
	if disabled {
		return "takeover is disabled"
	}
	if plan.Executor == scenario.ExecIterations {
		return "a fixed iteration count cannot be split mid-run"
	}
	sc, err := scenario.Parse(spec.Scenario)
	if err != nil {
		return err.Error()
	}
	for name, f := range sc.Data {
		if f.Mode == scenario.FeedUnique && len(f.Generate) == 0 {
			return fmt.Sprintf("data.%s is unique and the lost worker's progress through it is unknown", name)
		}
	}
	return ""
}

// members returns the run's members for use outside its goroutine.
func (r *Run) memberList() []*member {
	r.mmu.RLock()
	defer r.mmu.RUnlock()
	return append([]*member(nil), r.members...)
}

// abort undoes a Start that failed: workers that may have accepted are
// stopped and every worker is released.
func (r *Run) abort() {
	for _, m := range r.members {
		m.w.send(stopMsg(r.spec.ID, true, "the run could not start"))
	}
	r.release()
	close(r.done)
}

// release detaches the run's workers and forgets disconnected ones.
func (r *Run) release() {
	c := r.c
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range r.members {
		w := m.w
		w.mu.Lock()
		if w.run == r {
			w.run = nil
			if w.stream == nil {
				delete(c.workers, w.id)
			}
		}
		w.mu.Unlock()
	}
	if c.runs[r.spec.ID] == r {
		delete(c.runs, r.spec.ID)
	}
}

func plannedAt(plan *scenario.Plan, iv time.Duration) func(int64) float64 {
	return func(k int64) float64 {
		v := plan.ValueAt(time.Duration(k) * iv)
		if plan.Mode != scenario.ModeRate {
			// Mirrors the engine: planned users are whole.
			v = math.Round(v)
		}
		return v
	}
}

// ID is the run id.
func (r *Run) ID() string { return r.spec.ID }

// T0 is the synchronised start time in the server's clock.
func (r *Run) T0() time.Time { return r.t0 }

// Interval is the snapshot interval.
func (r *Run) Interval() time.Duration { return r.iv }

// Snapshots delivers one merged snapshot per interval across workers, in
// order, and is closed when the run ends. Consumers should keep up; if
// the buffer (sized for the whole plan) ever fills, live snapshots are
// dropped, and Result.Snapshots still holds every interval.
func (r *Run) Snapshots() <-chan *metrics.Snapshot { return r.snaps }

// Events delivers run events and is closed when the run ends. Events
// that do not fit the buffer are dropped; the Result records the same
// facts.
func (r *Run) Events() <-chan RunEvent { return r.events }

// Done is closed when the run has ended and its Result is ready.
func (r *Run) Done() <-chan struct{} { return r.done }

// Stop ends the load phase gracefully: in-flight iterations get the
// scenario's graceful stop period.
func (r *Run) Stop() { r.StopWithReason(engine.StopRequested) }

// StopWithReason is Stop with a reason recorded as the stop reason, for
// example "breakpoint reached".
func (r *Run) StopWithReason(reason string) {
	r.ctl.Lock()
	if r.killed || !r.stopAt.IsZero() {
		r.ctl.Unlock()
		return
	}
	r.stopReason, r.stopAt = reason, time.Now()
	r.ctl.Unlock()
	for _, m := range r.memberList() {
		m.w.send(stopMsg(r.spec.ID, false, reason))
	}
}

// Kill stops all load immediately: the stop goes straight onto every
// worker's stream, and workers abandon in-flight requests.
func (r *Run) Kill() {
	r.ctl.Lock()
	if r.killed {
		r.ctl.Unlock()
		return
	}
	r.killed, r.stopReason = true, engine.StopKilled
	if r.stopAt.IsZero() {
		r.stopAt = time.Now()
	}
	r.ctl.Unlock()
	for _, m := range r.memberList() {
		m.w.send(stopMsg(r.spec.ID, true, engine.StopKilled))
	}
}

// Wait blocks until the run ends and returns its result. The error is
// set when no worker finished its share (every one failed or was lost);
// the result is still returned with whatever data arrived.
func (r *Run) Wait(ctx context.Context) (*Result, error) {
	select {
	case <-r.done:
		return r.result, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// deliver hands a worker message to the run.
func (r *Run) deliver(in inMsg) {
	select {
	case r.inbox <- in:
	case <-r.done:
	}
}

// reattached is called when a member reconnects mid-run.
func (r *Run) reattached(w *workerConn) {
	w.mu.Lock()
	idx := w.member
	w.mu.Unlock()
	r.deliver(inMsg{member: idx, reattach: true, at: time.Now()})
}

func (r *Run) allAccepted() bool {
	for _, m := range r.members {
		if m.state == memberPending {
			return false
		}
	}
	return true
}

func (r *Run) allDone() bool {
	for _, m := range r.members {
		if !m.state.terminal() {
			return false
		}
	}
	return true
}

func (r *Run) loop() {
	defer r.finish()
	every := min(max(r.c.cfg.HeartbeatInterval/4, 10*time.Millisecond), 100*time.Millisecond)
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case in := <-r.inbox:
			r.handle(in)
		case <-tick.C:
		}
		now := time.Now()
		r.checkLoss(now)
		r.checkDeadline(now)
		r.emit(r.merger.ready(now, r.expected, false))
		if r.allDone() {
			return
		}
	}
}

func (r *Run) ack(m *member, seq uint64, finished bool) {
	m.w.send(&workerv1.ServerMessage{Msg: &workerv1.ServerMessage_Ack{Ack: &workerv1.Ack{
		RunId: r.spec.ID, Seq: seq, Finished: finished,
	}}})
}

// handle applies one worker message to the run's state.
func (r *Run) handle(in inMsg) {
	m := r.members[in.member]
	if j := in.joined; j != nil {
		if j.err != nil {
			if !m.state.terminal() {
				m.state, m.errMsg = memberFailed, j.err.Error()
				r.event(RunEvent{Type: EventWorkerFailed, Interval: m.joinAt, Message: fmt.Sprintf("%s could not take over: %s", m.cand.name, m.errMsg)}, m)
			}
			return
		}
		m.offset, m.rtt = j.offset, j.rtt
		return
	}
	if in.reattach {
		if m.state == memberLost {
			// Too late: the run has already accounted for it as lost.
			m.w.send(stopMsg(r.spec.ID, true, "this worker was declared lost"))
		}
		return
	}
	switch msg := in.msg.GetMsg().(type) {
	case *workerv1.WorkerMessage_RunAccepted:
		if m.state == memberPending {
			m.state = memberAccepted
			r.event(RunEvent{Type: EventWorkerJoined, Message: fmt.Sprintf("%s accepted share [%.4f, %.4f)", m.cand.name, m.sh.lo, m.sh.hi)}, m)
		}
	case *workerv1.WorkerMessage_Snapshot:
		r.handleSnapshot(m, in.member, msg.Snapshot)
	case *workerv1.WorkerMessage_RunFinished:
		fin := msg.RunFinished
		r.ack(m, fin.GetLastSeq(), true)
		if m.state.terminal() {
			return
		}
		m.state, m.stopReason = memberFinished, fin.GetStopReason()
		m.peakVUs = int(fin.GetPeakVus())
		if end := fin.GetEndUnixNano(); end > 0 {
			m.end = time.Unix(0, end-int64(m.offset))
		}
		ph, err := wire.PhasesFromProto(fin.GetPhases())
		if err != nil {
			r.c.log.Error("bad phase histograms", "run", r.spec.ID, "worker", m.w.id, "error", err)
		} else {
			wire.MergePhases(r.phases, ph)
		}
		r.event(RunEvent{Type: EventWorkerFinished, Interval: m.lastInterval, Message: fmt.Sprintf("%s finished: %s", m.cand.name, m.stopReason)}, m)
	case *workerv1.WorkerMessage_RunFailed:
		if m.state.terminal() {
			return
		}
		m.state, m.errMsg = memberFailed, msg.RunFailed.GetError()
		r.event(RunEvent{Type: EventWorkerFailed, Interval: m.lastInterval, Message: fmt.Sprintf("%s failed: %s", m.cand.name, m.errMsg)}, m)
	}
}

func (r *Run) handleSnapshot(m *member, idx int, p *workerv1.Snapshot) {
	if m.state == memberLost || m.state == memberFailed {
		// Data after a loss is not counted: the run already reported
		// the window as missing.
		return
	}
	s, h, err := wire.SnapshotFromProto(m.w.id, p)
	if err != nil {
		r.c.log.Error("bad snapshot", "run", r.spec.ID, "worker", m.w.id, "error", err)
		return
	}
	dup, _ := r.merger.add(idx, s)
	r.ack(m, s.Seq, false)
	if dup {
		return
	}
	m.snapshots++
	m.lastInterval = max(m.lastInterval, s.Interval)
	for _, st := range s.Steps {
		m.requests += st.Requests
	}
	for _, j := range s.Journeys {
		m.iterations += j.Started
	}
	if !m.started {
		m.started = true
		r.event(RunEvent{Type: EventWorkerStarted, Interval: s.Interval, Message: m.cand.name + " is generating load"}, m)
	}
	if h != nil && h.Saturated {
		m.saturated = append(m.saturated, s.Interval)
		for _, why := range h.Reasons {
			m.satReasons[satKind(why)] = true
		}
		r.event(RunEvent{
			Type: EventWorkerSaturated, Interval: s.Interval, Reasons: h.Reasons,
			Message: fmt.Sprintf("%s is saturated: %s", m.cand.name, strings.Join(h.Reasons, "; ")),
		}, m)
	}
}

// satKind strips the figures from a saturation reason so the summary
// lists each kind once ("cpu", "scheduling lag", ...).
func satKind(reason string) string {
	for _, k := range []string{"cpu", "scheduling lag", "iterations dropped", "GC pause", "file descriptors"} {
		if strings.Contains(reason, k) {
			return k
		}
	}
	return reason
}

// lossAfter is how long a worker may be silent: three heartbeats plus
// half an interval of jitter.
func (r *Run) lossAfter() time.Duration {
	hb := r.c.cfg.HeartbeatInterval
	return 3*hb + hb/2
}

func (r *Run) checkLoss(now time.Time) {
	for _, m := range r.members {
		if m.state.terminal() {
			continue
		}
		if silent := now.Sub(m.w.seen()); silent > r.lossAfter() {
			r.markLost(m, fmt.Sprintf("no heartbeat for %s", silent.Round(time.Millisecond)))
		}
	}
}

// checkDeadline makes sure the run ends even if a worker keeps its
// connection but never finishes: it is killed, then declared lost.
func (r *Run) checkDeadline(now time.Time) {
	r.ctl.Lock()
	killed, stopAt := r.killed, r.stopAt
	r.ctl.Unlock()
	deadline := r.hardDeadline
	switch {
	case killed:
		deadline = minTime(deadline, stopAt.Add(5*time.Second))
	case !stopAt.IsZero():
		deadline = minTime(deadline, stopAt.Add(r.plan.GracefulStop+10*time.Second))
	}
	if now.Before(deadline) {
		return
	}
	for _, m := range r.members {
		if m.state.terminal() {
			continue
		}
		if m.killSentAt.IsZero() {
			m.killSentAt = now
			m.w.send(stopMsg(r.spec.ID, true, "the run overran its deadline"))
		} else if now.Sub(m.killSentAt) > 5*time.Second {
			r.markLost(m, "did not finish after being stopped")
		}
	}
}

func (r *Run) markLost(m *member, why string) {
	m.state = memberLost
	seen := m.w.seen()
	m.lostAt = 0
	if seen.After(r.t0) {
		m.lostAt = int64(seen.Sub(r.t0) / r.iv)
	}
	m.w.send(stopMsg(r.spec.ID, true, "this worker was declared lost"))
	r.c.log.Warn("worker lost", "run", r.spec.ID, "worker", m.w.id, "name", m.cand.name, "reason", why)
	r.event(RunEvent{Type: EventWorkerLost, Interval: m.lostAt, Message: fmt.Sprintf("%s lost: %s", m.cand.name, why)}, m)
	blocked := r.takeOver(m)
	if blocked == "" {
		return
	}
	var remaining float64
	for _, o := range r.members {
		if !o.state.terminal() || o.state == memberFinished {
			remaining += o.sh.hi - o.sh.lo
		}
	}
	r.event(RunEvent{
		Type: EventDegraded, Interval: m.lostAt,
		Message: fmt.Sprintf("%.1f%% of the load (the share of %s) is not reassigned (%s); the run continues at %.1f%% of plan",
			100*(m.sh.hi-m.sh.lo), m.cand.name, blocked, 100*remaining),
	}, m)
}

// takeOver hands a lost member's share to a spare worker, which starts
// at the next interval boundary at least TakeoverLead away. It returns
// why it could not, or "" once the spare is on its way.
func (r *Run) takeOver(lost *member) string {
	if r.noTakeover != "" {
		return r.noTakeover
	}
	r.ctl.Lock()
	stopping := r.killed || !r.stopAt.IsZero()
	r.ctl.Unlock()
	if stopping {
		return "the run is stopping"
	}
	resume := time.Since(r.t0) + r.c.cfg.TakeoverLead
	resume = (resume + r.iv - 1) / r.iv * r.iv
	if total := r.plan.TotalDuration(); total > 0 && resume >= total {
		return "too little of the run is left"
	}
	idx := len(r.members)
	w, cand := r.c.spare(r, idx, lost.cand.region, len(r.spec.Regions) > 0)
	if w == nil {
		return "no spare worker is connected"
	}
	m := &member{
		w: w, cand: cand, sh: lost.sh, lastInterval: -1, lostAt: -1, satReasons: map[string]bool{},
		dataIndex: lost.dataIndex, joinAt: int64(resume / r.iv), attempt: lost.attempt + 1,
		replaces: r.indexOf(lost), replacedBy: -1, resume: resume,
	}
	lost.replacedBy = idx
	r.mmu.Lock()
	r.members = append(r.members, m)
	r.mmu.Unlock()
	r.merger.grow()
	r.event(RunEvent{
		Type: EventTakeover, Interval: m.joinAt,
		Message: fmt.Sprintf("%s takes over the %.1f%% share of %s from %s", cand.name, 100*(lost.sh.hi-lost.sh.lo), lost.cand.name, resume),
	}, m)
	go r.startTakeover(m, idx)
	return ""
}

func (r *Run) indexOf(m *member) int {
	for i, o := range r.members {
		if o == m {
			return i
		}
	}
	return -1
}

// startTakeover synchronises the spare's clock and sends it the lost
// share. The outcome of the clock sync reaches the run's goroutine
// through the inbox, ahead of the spare's own messages.
func (r *Run) startTakeover(m *member, idx int) {
	ctx, cancel := context.WithTimeout(context.Background(), r.c.cfg.TakeoverLead)
	defer cancel()
	offset, rtt, err := m.w.syncClock(ctx, r.c.cfg.ClockSamples)
	start := &workerv1.StartRun{
		RunId: r.spec.ID, Scenario: r.spec.Scenario, Env: r.spec.Env, Secrets: r.spec.Secrets,
		ShareLo: m.sh.lo, ShareHi: m.sh.hi, WorkerIndex: uint32(m.dataIndex), WorkerCount: uint32(r.dataCount),
		T0UnixNano: r.t0.Add(offset).UnixNano(), AllowHosts: r.spec.AllowHosts, IntervalNs: int64(r.iv),
		ResumeNs: int64(m.resume), Attempt: uint32(m.attempt),
	}
	if err == nil && !m.w.send(&workerv1.ServerMessage{Msg: &workerv1.ServerMessage_StartRun{StartRun: start}}) {
		err = errNotConnected(m.cand.name)
	}
	if err != nil {
		err = fmt.Errorf("clock synchronisation: %w", err)
	}
	// handle accepts this before or after the spare's own replies.
	r.deliver(inMsg{member: idx, joined: &joinResult{offset: offset, rtt: rtt, err: err}, at: time.Now()})
}

// expected reports whether member i should still report interval k.
func (r *Run) expected(i int, k int64) bool {
	m := r.members[i]
	if k < m.joinAt {
		return false
	}
	switch m.state {
	case memberLost, memberFailed:
		return false
	case memberFinished:
		return k <= m.lastInterval
	}
	return true
}

func (r *Run) emit(list []emitted) {
	for _, e := range list {
		if len(e.missing) > 0 {
			var names, ids []string
			for _, i := range e.missing {
				m := r.members[i]
				m.missing = append(m.missing, e.snap.Interval)
				names = append(names, m.cand.name)
				ids = append(ids, m.w.id)
			}
			r.event(RunEvent{
				Type: EventIntervalIncomplete, Interval: e.snap.Interval, Workers: ids,
				Message: fmt.Sprintf("interval %d emitted without %s", e.snap.Interval, strings.Join(names, ", ")),
			}, nil)
		}
		select {
		case r.snaps <- e.snap:
		default:
			r.c.log.Warn("snapshot consumer is not keeping up; dropping a live snapshot", "run", r.spec.ID, "interval", e.snap.Interval)
		}
	}
}

func (r *Run) event(ev RunEvent, m *member) {
	ev.Time = time.Now()
	if m != nil {
		ev.Worker, ev.WorkerName = m.w.id, m.cand.name
	}
	select {
	case r.events <- ev:
	default:
	}
}

// finish emits what is left, builds the result and releases the workers.
func (r *Run) finish() {
	now := time.Now()
	r.emit(r.merger.ready(now, r.expected, true))
	snaps := r.merger.all()
	last := int64(-1)
	if len(snaps) > 0 {
		last = snaps[len(snaps)-1].Interval
	}

	res := &Result{
		RunID: r.spec.ID, T0: r.t0, Interval: r.iv, Phases: r.phases,
		Snapshots: snaps, LateSnapshots: r.merger.late,
	}
	var finished int
	var reasons []string
	for i, m := range r.members {
		ws := WorkerSummary{
			ID: m.w.id, Name: m.cand.name, Region: m.cand.region, Index: i,
			ShareLo: m.sh.lo, ShareHi: m.sh.hi, ClockOffset: m.offset, ClockRTT: m.rtt,
			StopReason: m.stopReason, Error: m.errMsg, PeakVUs: m.peakVUs,
			Requests: m.requests, Iterations: m.iterations, Snapshots: m.snapshots,
			Saturated: windows(m.saturated), Missing: m.missing,
		}
		if m.replaces >= 0 {
			ws.Replaces, ws.JoinedAt = r.members[m.replaces].cand.name, m.joinAt
		}
		for k := range m.satReasons {
			ws.SaturationReasons = append(ws.SaturationReasons, k)
		}
		sort.Strings(ws.SaturationReasons)
		switch m.state {
		case memberFinished:
			ws.State = WorkerFinished
			finished++
			if m.end.After(res.End) {
				res.End = m.end
			}
			if m.stopReason != "" && m.stopReason != engine.StopCompleted {
				reasons = append(reasons, m.stopReason)
			}
		case memberFailed:
			ws.State = WorkerFailed
			res.Degraded = true
		default:
			ws.State = WorkerLost
			ws.Lost = &Window{From: m.lostAt, To: max(last, m.lostAt)}
			if m.replacedBy >= 0 {
				if s := r.members[m.replacedBy]; s.started {
					ws.Lost.To = max(s.joinAt-1, m.lostAt)
				}
			}
			res.Degraded = true
		}
		res.PeakVUs += m.peakVUs
		res.Workers = append(res.Workers, ws)
	}
	if res.End.IsZero() {
		res.End = now
	}

	r.ctl.Lock()
	res.StopReason = r.stopReason
	r.ctl.Unlock()
	switch {
	case res.StopReason != "":
	case finished == 0:
		res.StopReason = "all workers failed or were lost"
	case len(reasons) > 0:
		res.StopReason = reasons[0]
	default:
		res.StopReason = engine.StopCompleted
	}
	if finished == 0 {
		var why []string
		for _, ws := range res.Workers {
			w := ws.Name + " " + ws.State
			if ws.Error != "" {
				w += ": " + ws.Error
			}
			why = append(why, w)
		}
		r.err = fmt.Errorf("no worker finished the run (%s)", strings.Join(why, "; "))
	}
	r.result = res
	r.c.log.Info("run finished", "run", r.spec.ID, "reason", res.StopReason, "degraded", res.Degraded)

	r.release()
	close(r.snaps)
	close(r.events)
	close(r.done)
}

// windows compresses sorted interval numbers into inclusive ranges.
func windows(ks []int64) []Window {
	if len(ks) == 0 {
		return nil
	}
	ks = append([]int64(nil), ks...)
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	out := []Window{{From: ks[0], To: ks[0]}}
	for _, k := range ks[1:] {
		if last := &out[len(out)-1]; k <= last.To+1 {
			last.To = max(last.To, k)
		} else {
			out = append(out, Window{From: k, To: k})
		}
	}
	return out
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
