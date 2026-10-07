// Package engine executes a compiled scenario: it runs virtual users under
// a load plan, records every request and emits one metrics snapshot per
// interval.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/pluginhost"
	"github.com/Ivan825/Stampede/internal/protocol/grpcx"
	"github.com/Ivan825/Stampede/internal/protocol/httpx"
	"github.com/Ivan825/Stampede/internal/protocol/netem"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/version"
)

// Options configures an engine.
type Options struct {
	Program *scenario.Program
	Plan    *scenario.Plan
	RunID   string

	// Env and Secrets back ${env.X} and ${secret.X}.
	Env     map[string]string
	Secrets map[string]string

	// ShareLo and ShareHi select this engine's slice of the total load as
	// cumulative fractions. A single engine uses 0 and 1. Workers with
	// slices [0, .5) and [.5, 1) together produce exactly the plan.
	ShareLo, ShareHi float64
	// WorkerIndex and WorkerCount partition unique test data.
	WorkerIndex, WorkerCount int

	// T0 is the synchronised start time. Zero starts immediately.
	T0 time.Time
	// Resume starts part-way through the plan, at T0 + Resume: the worker
	// takes over a lost worker's share. Arrivals before then are skipped.
	Resume time.Duration
	// Attempt counts takeovers of this share; generated data mixes it in
	// so values stay unique.
	Attempt int
	// Interval between snapshots (default 1s).
	Interval time.Duration
	// OnSnapshot receives each interval's metrics. It must not block for long.
	OnSnapshot func(*metrics.Snapshot)
	// AllowHost vets every request URL. Nil allows all hosts.
	AllowHost func(*url.URL) bool
	// MaxBodyBytes caps response bytes kept for checks and extraction
	// (default 10 MiB).
	MaxBodyBytes int64
	// Transport overrides the HTTP dialer (tests, network emulation).
	HTTP httpx.Options
	// PluginDir is where plugin executables are looked for before PATH
	// (default: pluginhost.Dir()).
	PluginDir string
	Logger    *slog.Logger
}

// Result describes a finished run.
type Result struct {
	T0         time.Time
	End        time.Time
	StopReason string
	Phases     map[int]*[metrics.NumPhases]*metrics.Histogram
	PeakVUs    int
}

// Stop reasons.
const (
	StopCompleted = "completed"
	StopRequested = "stopped"
	StopKilled    = "killed"
)

// Engine runs one scenario.
type Engine struct {
	opts      Options
	prog      *scenario.Program
	plan      *scenario.Plan
	collector *metrics.Collector
	log       *slog.Logger

	feeders         map[string]*feeder
	journeyFeeders  [][]string
	sharedTransport *http.Transport
	// wsTransport makes WebSocket handshakes. It never negotiates HTTP/2,
	// which cannot carry a WebSocket upgrade.
	wsTransport *http.Transport
	// grpcPool, grpcDescs and grpcSteps serve grpc steps; nil without any.
	grpcPool  *grpcx.Pool
	grpcDescs *grpcx.Descriptors
	grpcSteps map[int]*grpcStep
	// plugins serve plugin steps; nil without any.
	plugins pluginhost.Set
	// browser is the headless Chrome of browser steps, started on first use.
	browser    browserHost
	pluginOnce sync.Once
	httpOpts   httpx.Options
	baseURL    string
	headers    []headerKV
	userAgent  string
	maxBody    int64
	allowHost  func(*url.URL) bool

	t0        time.Time
	activeVUs atomic.Int64
	peakVUs   atomic.Int64

	stopOnce sync.Once
	stopCh   chan struct{}
	// earlyCh closes when the run is stopped before its planned end.
	earlyCh    chan struct{}
	stopReason atomic.Value
	killOnce   sync.Once
	killCh     chan struct{}

	errMu     sync.Mutex
	errLogged map[int]time.Time
	// exMu guards examples, the error examples kept per step and class.
	exMu     sync.Mutex
	examples map[exampleKey]int
}

type headerKV struct{ Name, Value string }

var dataRefRe = regexp.MustCompile(`\bdata\.([A-Za-z_][A-Za-z0-9_]*)`)

// New prepares an engine: loads feeders, builds HTTP transports and
// renders the base URL.
func New(opts Options) (*Engine, error) {
	if opts.Program == nil || opts.Plan == nil {
		return nil, errors.New("engine: program and plan are required")
	}
	if opts.ShareHi == 0 && opts.ShareLo == 0 {
		opts.ShareHi = 1
	}
	if opts.ShareLo < 0 || opts.ShareHi > 1 || opts.ShareLo >= opts.ShareHi {
		return nil, fmt.Errorf("engine: invalid load share [%v, %v)", opts.ShareLo, opts.ShareHi)
	}
	if opts.WorkerCount < 1 {
		opts.WorkerCount, opts.WorkerIndex = 1, 0
	}
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = 10 << 20
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Env == nil {
		opts.Env = map[string]string{}
	}
	if opts.Secrets == nil {
		opts.Secrets = map[string]string{}
	}
	s := opts.Program.Scenario
	e := &Engine{
		opts:      opts,
		prog:      opts.Program,
		plan:      opts.Plan,
		collector: metrics.NewCollector(runtime.GOMAXPROCS(0) * 4),
		log:       opts.Logger,
		feeders:   map[string]*feeder{},
		maxBody:   opts.MaxBodyBytes,
		allowHost: opts.AllowHost,
		userAgent: "stampede/" + version.Version,
		stopCh:    make(chan struct{}),
		earlyCh:   make(chan struct{}),
		killCh:    make(chan struct{}),
		errLogged: map[int]time.Time{},
	}

	for name, f := range s.Data {
		fd, err := loadFeeder(context.Background(), name, f, opts.WorkerIndex, opts.WorkerCount, opts.Attempt, opts.Env, opts.Secrets, opts.AllowHost)
		if err != nil {
			return nil, err
		}
		e.feeders[name] = fd
	}
	e.journeyFeeders = make([][]string, len(e.prog.Journeys))
	for i, j := range e.prog.Journeys {
		seen := map[string]bool{}
		walkCompiled(j.Steps, func(src string) {
			for _, m := range dataRefRe.FindAllStringSubmatch(src, -1) {
				if _, ok := e.feeders[m[1]]; ok && !seen[m[1]] {
					seen[m[1]] = true
					e.journeyFeeders[i] = append(e.journeyFeeders[i], m[1])
				}
			}
		})
		sort.Strings(e.journeyFeeders[i])
	}

	h := s.Target.HTTP
	e.httpOpts = opts.HTTP
	e.httpOpts.HTTP2 = e.httpOpts.HTTP2 || h.HTTP2
	e.httpOpts.H2C = e.httpOpts.H2C || h.H2C
	e.httpOpts.DisableKeepAlive = e.httpOpts.DisableKeepAlive || h.DisableKeepAlive
	e.httpOpts.InsecureSkipVerify = e.httpOpts.InsecureSkipVerify || h.InsecureSkipVerify
	if e.httpOpts.MaxRedirects == 0 {
		e.httpOpts.MaxRedirects = h.MaxRedirects
	}
	switch h.TLSResumption {
	case "off", "shared":
		e.httpOpts.TLSResumption = h.TLSResumption
	default:
		// Per user, unless the users share one connection pool.
		if h.Connections != "shared" {
			e.httpOpts.TLSResumption = "per-transport"
		}
	}
	if e.httpOpts.DialContext == nil && e.httpOpts.DNS == nil {
		ttl := 30 * time.Second
		if h.DNSCacheTTL != nil {
			ttl = h.DNSCacheTTL.D()
		}
		if ttl > 0 {
			e.httpOpts.DNS = httpx.NewDNSCache(ttl)
		}
	}
	if n := s.Target.Network; n != nil && e.httpOpts.DialContext == nil {
		p, err := n.Resolve()
		if err != nil {
			return nil, fmt.Errorf("target.network: %w", err)
		}
		d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		base := d.DialContext
		if e.httpOpts.DNS != nil {
			base = e.httpOpts.DNS.DialContext(d)
		}
		e.httpOpts.DialContext = netem.Dialer(p, base)
	}
	if h.Connections == "shared" {
		e.sharedTransport = httpx.NewTransport(e.httpOpts)
	}
	wsOpts := e.httpOpts
	wsOpts.HTTP2, wsOpts.H2C = false, false
	e.wsTransport = httpx.NewTransport(wsOpts)

	if e.prog.BaseURL != nil {
		v := &vuVars{env: opts.Env, secret: opts.Secrets, static: s.Vars}
		v.reset(0)
		base, err := e.prog.BaseURL.Render(v)
		if err != nil {
			return nil, fmt.Errorf("target.baseURL: %w", err)
		}
		if base != "" {
			u, err := url.Parse(base)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return nil, fmt.Errorf("target.baseURL %q is not an absolute http(s) URL", base)
			}
		}
		e.baseURL = base
	}
	static := &vuVars{env: opts.Env, secret: opts.Secrets, static: s.Vars}
	static.reset(0)
	if err := e.prepareGRPC(static); err != nil {
		return nil, err
	}
	if err := e.preparePlugins(); err != nil {
		return nil, err
	}
	if e.usesBrowser() {
		if _, err := FindChrome(); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// BaseURL is the rendered target base URL.
func (e *Engine) BaseURL() string { return e.baseURL }

// walkCompiled visits the template sources in a compiled step tree.
func walkCompiled(steps []*scenario.CStep, fn func(string)) {
	for _, st := range steps {
		if st.If != nil {
			fn(st.If.String())
		}
		if r := st.Req; r != nil {
			if r.URL != nil {
				fn(r.URL.String())
			}
			for _, kvs := range [][]scenario.KV{r.Headers, r.Query, r.Form} {
				for _, kv := range kvs {
					if kv.Value != nil {
						fn(kv.Value.String())
					}
				}
			}
			if r.Body != nil {
				fn(r.Body.String())
			}
			if r.JSON != nil {
				fn(r.JSON.Source())
			}
		}
		if sd := st.Send; sd != nil {
			if sd.Text != nil {
				fn(sd.Text.String())
			}
			if sd.JSON != nil {
				fn(sd.JSON.Source())
			}
		}
		if g := st.GRPC; g != nil {
			if g.Message != nil {
				fn(g.Message.Source())
			}
			for _, kv := range g.Metadata {
				fn(kv.Value.String())
			}
		}
		if p := st.Plugin; p != nil && p.With != nil {
			fn(p.With.Source())
		}
		if g := st.GraphQL; g != nil {
			if g.Query != nil {
				fn(g.Query.String())
			}
			if g.Variables != nil {
				fn(g.Variables.Source())
			}
		}
		if st.Loop != nil && st.Loop.Cond != nil {
			fn(st.Loop.Cond.String())
		}
		for _, b := range st.Branches {
			walkCompiled(b.Steps, fn)
		}
		walkCompiled(st.Steps, fn)
	}
}

// Stop ends the load phase; in-flight iterations get the graceful stop
// period to finish.
func (e *Engine) Stop(reason string) {
	e.stopOnce.Do(func() {
		if reason == "" {
			reason = StopRequested
		}
		e.stopReason.Store(reason)
		if reason != StopCompleted {
			close(e.earlyCh)
		}
		close(e.stopCh)
	})
}

// Kill stops all load immediately, abandoning in-flight requests.
func (e *Engine) Kill() {
	e.Stop(StopKilled)
	e.killOnce.Do(func() { close(e.killCh) })
}

// ActiveVUs is the number of virtual users currently running.
func (e *Engine) ActiveVUs() int { return int(e.activeVUs.Load()) }

// share returns this engine's part of a planned value (users or iterations).
func (e *Engine) shareCount(v float64) int {
	return int(math.Round(v*e.opts.ShareHi)) - int(math.Round(v*e.opts.ShareLo))
}

func (e *Engine) shareFrac() float64 { return e.opts.ShareHi - e.opts.ShareLo }

// Run executes the plan and blocks until all load has stopped.
func (e *Engine) Run(ctx context.Context) (*Result, error) {
	defer e.closePlugins()
	defer e.closeBrowser()
	e.t0 = e.opts.T0
	if e.t0.IsZero() {
		e.t0 = time.Now()
	}
	if err := sleepCtx(ctx, time.Until(e.t0.Add(e.opts.Resume))); err != nil {
		return nil, err
	}

	total := e.plan.TotalDuration()
	// loadCtx ends when no new iterations may start; iterCtx ends when
	// in-flight iterations must be abandoned.
	loadCtx, loadCancel := context.WithCancel(ctx)
	iterCtx, iterCancel := context.WithCancel(context.Background())
	defer loadCancel()
	defer iterCancel()

	go func() {
		var deadline <-chan time.Time
		if total > 0 {
			t := time.NewTimer(time.Until(e.t0.Add(total)))
			defer t.Stop()
			deadline = t.C
		}
		select {
		case <-deadline:
			e.Stop(StopCompleted)
		case <-e.stopCh:
		case <-ctx.Done():
			e.Stop(StopRequested)
		}
		loadCancel()
		g := time.NewTimer(e.plan.GracefulStop)
		defer g.Stop()
		select {
		case <-g.C:
		case <-e.killCh:
		case <-ctx.Done():
		case <-iterCtx.Done():
		}
		iterCancel()
	}()

	flushDone := make(chan struct{})
	tickerStop := make(chan struct{})
	go e.flushLoop(tickerStop, flushDone)

	var err error
	switch e.plan.Executor {
	case scenario.ExecConstantVUs, scenario.ExecRampingVUs:
		e.runClosed(loadCtx, iterCtx)
	case scenario.ExecIterations:
		e.runIterations(loadCtx, iterCtx)
	case scenario.ExecConstantRate, scenario.ExecRampingRate, scenario.ExecReplay:
		e.runOpen(loadCtx, iterCtx)
	default:
		err = fmt.Errorf("unknown executor %q", e.plan.Executor)
	}
	e.Stop(StopCompleted)
	iterCancel()
	close(tickerStop)
	<-flushDone
	if e.grpcPool != nil {
		e.grpcPool.Close()
	}

	reason, _ := e.stopReason.Load().(string)
	return &Result{
		T0: e.t0, End: time.Now(), StopReason: reason,
		Phases: e.collector.PhaseHistograms(), PeakVUs: int(e.peakVUs.Load()),
	}, err
}

// flushLoop emits a snapshot at every interval boundary after T0, plus a
// final partial one when the run ends.
func (e *Engine) flushLoop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	// A takeover reports from the interval it resumes in.
	k := int64(e.opts.Resume/e.opts.Interval) + 1
	for {
		t := time.NewTimer(time.Until(e.t0.Add(time.Duration(k) * e.opts.Interval)))
		select {
		case <-t.C:
			e.emit(k - 1)
			k++
		case <-stop:
			t.Stop()
			e.emit(k - 1)
			return
		}
	}
}

func (e *Engine) emit(interval int64) {
	elapsed := time.Duration(interval) * e.opts.Interval
	planned := e.plan.ValueAt(elapsed)
	if e.plan.Mode == scenario.ModeRate {
		planned *= e.shareFrac()
	} else {
		planned = float64(e.shareCount(planned))
	}
	s := e.collector.Flush(interval, e.ActiveVUs(), planned)
	if e.opts.OnSnapshot != nil {
		e.opts.OnSnapshot(s)
	}
}

func (e *Engine) vuStarted() {
	n := e.activeVUs.Add(1)
	for {
		p := e.peakVUs.Load()
		if n <= p || e.peakVUs.CompareAndSwap(p, n) {
			return
		}
	}
}

// handleIterErr reacts to errors that should end the whole run.
func (e *Engine) handleIterErr(err error) bool {
	if errors.Is(err, errDataExhausted) {
		e.log.Warn("unique test data exhausted; stopping the run")
		e.Stop("test data exhausted")
		return true
	}
	return false
}

// runClosed runs the closed model: a population of users that loop
// through journeys, resized every 100ms to follow the plan.
func (e *Engine) runClosed(loadCtx, iterCtx context.Context) {
	type worker struct {
		retire atomic.Bool
	}
	var (
		wg      sync.WaitGroup
		workers []*worker
		nextID  int
	)
	spawn := func() {
		w := &worker{}
		workers = append(workers, w)
		id := e.opts.WorkerIndex*1_000_000 + nextID
		nextID++
		wg.Add(1)
		e.vuStarted()
		go func() {
			defer wg.Done()
			defer e.activeVUs.Add(-1)
			vu := newVU(e, id)
			defer vu.close()
			for !w.retire.Load() && loadCtx.Err() == nil {
				if err := vu.runIteration(iterCtx, time.Time{}); err != nil {
					if e.handleIterErr(err) || iterCtx.Err() != nil {
						return
					}
				}
			}
		}()
	}
	resize := func() {
		want := e.shareCount(e.plan.ValueAt(time.Since(e.t0)))
		live := 0
		for _, w := range workers {
			if !w.retire.Load() {
				live++
			}
		}
		for ; live < want; live++ {
			spawn()
		}
		for i := len(workers) - 1; i >= 0 && live > want; i-- {
			if !workers[i].retire.Load() {
				workers[i].retire.Store(true)
				live--
			}
		}
	}

	resize()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for loadCtx.Err() == nil {
		select {
		case <-tick.C:
			if e.plan.Executor == scenario.ExecRampingVUs {
				resize()
			}
		case <-loadCtx.Done():
		}
	}
	wg.Wait()
}

// runIterations runs a fixed number of iterations shared by the users.
func (e *Engine) runIterations(loadCtx, iterCtx context.Context) {
	remaining := atomic.Int64{}
	remaining.Store(int64(e.shareCount(float64(e.plan.Iterations))))
	vus := e.shareCount(float64(e.plan.VUs))
	if vus < 1 && remaining.Load() > 0 {
		vus = 1
	}
	var wg sync.WaitGroup
	for i := 0; i < vus; i++ {
		wg.Add(1)
		e.vuStarted()
		go func(id int) {
			defer wg.Done()
			defer e.activeVUs.Add(-1)
			vu := newVU(e, e.opts.WorkerIndex*1_000_000+id)
			defer vu.close()
			for loadCtx.Err() == nil && remaining.Add(-1) >= 0 {
				if err := vu.runIteration(iterCtx, time.Time{}); err != nil && (e.handleIterErr(err) || iterCtx.Err() != nil) {
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

// runOpen runs the open model: iterations start on a fixed schedule
// regardless of how the target responds. Each iteration carries its
// intended start time so latency includes any time spent waiting.
func (e *Engine) runOpen(_, iterCtx context.Context) {
	type job struct {
		intended time.Time
		replay   *scenario.Arrival
	}
	type worker struct {
		vu   *VU
		work chan job
	}
	maxVUs := e.shareCount(float64(e.plan.MaxVUs))
	if maxVUs < 1 {
		maxVUs = 1
	}
	idle := make(chan *worker, maxVUs)
	var wg sync.WaitGroup
	created := 0
	spawn := func() *worker {
		w := &worker{vu: newVU(e, e.opts.WorkerIndex*1_000_000+created), work: make(chan job, 1)}
		created++
		wg.Add(1)
		e.vuStarted()
		go func() {
			defer wg.Done()
			defer e.activeVUs.Add(-1)
			defer w.vu.close()
			for jb := range w.work {
				var err error
				if jb.replay != nil {
					err = w.vu.runReplay(iterCtx, jb.intended, jb.replay)
				} else {
					err = w.vu.runIteration(iterCtx, jb.intended)
				}
				if err != nil {
					e.handleIterErr(err)
				}
				idle <- w
			}
		}()
		return w
	}
	defer func() {
		// Close every worker's queue so goroutines exit once idle.
		// In-flight iterations return their worker to idle when they finish
		// or when iterCtx is cancelled after the graceful stop period.
		for closed := 0; closed < created; closed++ {
			w := <-idle
			close(w.work)
		}
		wg.Wait()
	}()

	sched := newArrivalSchedule(e.plan)
	var rec *scenario.Recording
	if e.plan.Executor == scenario.ExecReplay {
		rec = e.prog.Scenario.Load.Replay.Recording()
	}
	lo, hi := e.opts.ShareLo, e.opts.ShareHi
	waiter := newPreciseWaiter()
	for k := uint64(0); ; k++ {
		var at time.Duration
		var arrival *scenario.Arrival
		if rec != nil {
			if k >= uint64(len(rec.Arrivals)) {
				return
			}
			arrival = &rec.Arrivals[k]
			at = arrival.At
		} else {
			var ok bool
			if at, ok = sched.next(); !ok {
				return
			}
		}
		// Low-discrepancy assignment: arrival k belongs to the worker whose
		// slice contains frac(k*phi). Slices of all workers cover [0, 1).
		if hi-lo < 1 {
			f := math.Mod(float64(k)*0.6180339887498949, 1)
			if f < lo || f >= hi {
				continue
			}
		}
		if at < e.opts.Resume {
			// Before a takeover: the lost worker's part.
			continue
		}
		intended := e.t0.Add(at)
		// Only an early stop ends dispatch. Arrivals scheduled before the
		// planned end are sent even if the dispatcher reaches them after
		// it; their lateness is part of the measured latency.
		if !waiter.wait(intended, e.earlyCh) {
			return
		}
		var w *worker
		select {
		case w = <-idle:
		default:
			if created < maxVUs {
				w = spawn()
			}
		}
		if w == nil {
			e.collector.Dropped(1)
			continue
		}
		w.work <- job{intended: intended, replay: arrival}
	}
}

func (e *Engine) logStepError(st *scenario.CStep, err error) {
	e.errMu.Lock()
	last := e.errLogged[st.ID]
	now := time.Now()
	if now.Sub(last) < 5*time.Second {
		e.errMu.Unlock()
		return
	}
	e.errLogged[st.ID] = now
	e.errMu.Unlock()
	e.log.Warn("step failed", "journey", st.Journey, "step", st.Name, "error", err)
}
