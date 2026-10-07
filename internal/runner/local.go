// Package runner runs a scenario in-process (no server or database) and
// produces a report. It backs `stampede run`.
package runner

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/Ivan825/Stampede/internal/engine"
	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// Options configures a local run.
type Options struct {
	Scenario  *scenario.Scenario
	RunID     string
	Env       map[string]string
	Secrets   map[string]string
	AllowHost func(*url.URL) bool
	Logger    *slog.Logger
	// Progress receives each interval's merged snapshot for live display.
	Progress func(p Progress)
	// Faults, when set, injects the scenario's fault timeline through a
	// stampede agent during the run. It is checked before any load.
	Faults *FaultPlan
}

// Progress is a live update.
type Progress struct {
	Elapsed  time.Duration
	Total    time.Duration
	Snapshot *metrics.Snapshot
	Planned  float64
	Mode     string
}

// Run executes the scenario and returns its report.
func Run(ctx context.Context, o Options) (*report.Report, error) {
	prog, err := scenario.Compile(o.Scenario)
	if err != nil {
		return nil, err
	}
	plan, err := o.Scenario.Load.Plan()
	if err != nil {
		return nil, err
	}
	if plan.StopOnFail && len(prog.Thresholds) == 0 {
		return nil, ErrNoTargets
	}

	var (
		mu    sync.Mutex
		snaps []*metrics.Snapshot
		bp    *BreakpointTracker
		eng   *engine.Engine
	)
	if plan.StopOnFail {
		bp = NewBreakpointTracker(prog, plan)
	}
	abort := NewAbortWatcher(o.Scenario.Load.Abort, nil, time.Second)
	logger := o.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if o.Faults != nil {
		if err := o.Faults.Check(ctx); err != nil {
			return nil, err
		}
	}
	self := NewSelfMonitor()
	defer self.Close()
	start := time.Now()
	onSnap := func(s *metrics.Snapshot) {
		mu.Lock()
		snaps = append(snaps, s)
		mu.Unlock()
		self.Observe(s)
		if bp != nil && bp.Observe(s) && eng != nil {
			eng.Stop("breakpoint reached")
		}
		if reason := abort.Observe(s); reason != "" && eng != nil {
			eng.Stop(reason)
		}
		if o.Progress != nil {
			o.Progress(Progress{
				Elapsed: time.Duration(s.Interval+1) * time.Second, Total: plan.TotalDuration(),
				Snapshot: s, Planned: s.Planned, Mode: plan.Mode,
			})
		}
	}

	eng, err = engine.New(engine.Options{
		Program: prog, Plan: plan, RunID: o.RunID,
		Env: o.Env, Secrets: o.Secrets, AllowHost: o.AllowHost,
		OnSnapshot: onSnap, Logger: o.Logger, T0: start,
	})
	if err != nil {
		return nil, err
	}
	var faults *FaultRun
	if o.Faults != nil {
		faults = o.Faults.Start(ctx, start, o.RunID, logger)
	}
	res, err := eng.Run(ctx)
	var events []report.FaultEvent
	if faults != nil {
		events = faults.Stop(time.Now())
	}
	if err != nil {
		return nil, err
	}

	mu.Lock()
	defer mu.Unlock()
	in := report.Input{
		RunID: o.RunID, Program: prog, Plan: plan, Target: eng.BaseURL(),
		Started: res.T0, Ended: res.End, StopReason: res.StopReason,
		PeakVUs: res.PeakVUs, Workers: 1, Snapshots: snaps, Phases: res.Phases,
	}
	if bp != nil {
		in.Breakpoint = bp.Result()
	}
	rep := report.Build(in)
	rep.Faults = events
	self.Close()
	self.Annotate(rep, time.Second)
	return rep, nil
}

// BreakpointTracker evaluates targets at the end of each hold stage of a
// breakpoint plan and reports when a level fails. Feed it merged
// snapshots in interval order.
type BreakpointTracker struct {
	prog   *scenario.Program
	plan   *scenario.Plan
	levels []level
	cur    int
	window *metrics.Snapshot
	last   float64
	fail   *level
	failOn []string
}

type level struct {
	from, to time.Duration // the hold part of a step
	value    float64
}

// NewBreakpointTracker builds a tracker for a breakpoint plan.
func NewBreakpointTracker(prog *scenario.Program, plan *scenario.Plan) *BreakpointTracker {
	b := &BreakpointTracker{prog: prog, plan: plan}
	var at time.Duration
	// Breakpoint stages come in pairs: a short ramp then a hold.
	for i, s := range plan.Stages {
		if i%2 == 1 {
			b.levels = append(b.levels, level{from: at, to: at + s.Duration, value: s.Target})
		}
		at += s.Duration
	}
	b.window = metrics.NewSnapshot(0)
	return b
}

// Observe adds an interval and returns true once a level has failed.
func (b *BreakpointTracker) Observe(s *metrics.Snapshot) bool {
	if b.fail != nil || b.cur >= len(b.levels) {
		return b.fail != nil
	}
	t := time.Duration(s.Interval) * time.Second
	lv := b.levels[b.cur]
	if t < lv.from {
		return false
	}
	b.window.Merge(s)
	if t+time.Second < lv.to {
		return false
	}
	// Level complete: evaluate on the hold window only.
	dur := (lv.to - lv.from).Seconds()
	checks := report.Evaluate(b.prog, b.prog.Thresholds, b.window, dur)
	b.window = metrics.NewSnapshot(0)
	b.cur++
	var failed []string
	for _, c := range checks {
		if !c.Pass {
			failed = append(failed, c.Source)
		}
	}
	if len(failed) > 0 {
		sort.Strings(failed)
		b.fail, b.failOn = &lv, failed
		return true
	}
	b.last = lv.value
	return false
}

// Result summarises the search so far.
func (b *BreakpointTracker) Result() *report.Breakpoint {
	unit := report.Unit(b.plan.Mode)
	r := &report.Breakpoint{LastPass: b.last, Unit: unit}
	if b.fail != nil {
		r.Found, r.FirstFail, r.FailedOn = true, b.fail.value, b.failOn
	}
	return r
}

// ErrNoTargets explains that a breakpoint run needs targets.
var ErrNoTargets = fmt.Errorf("the breakpoint shape needs at least one target, for example targets: [\"http.p95 < 500ms\", \"errors < 1%%\"]")
