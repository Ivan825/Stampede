package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"cel.dev/cel-go/interpreter"

	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// DistributedOptions configures a run across workers.
type DistributedOptions struct {
	Coordinator *coordinator.Coordinator
	// Spec is the run to start. Spec.AllowHosts should name the verified
	// target host when it is public; private hosts are always allowed.
	Spec   coordinator.RunSpec
	Logger *slog.Logger
	// Progress receives each merged interval for live display.
	Progress func(Progress)
	// OnEvent receives run events: workers joining, saturating, being
	// lost, finishing.
	OnEvent func(coordinator.RunEvent)
	// OnStart receives the run once every worker accepted it, so the
	// caller can Stop or Kill it.
	OnStart func(*coordinator.Run)
}

// RunDistributed runs a scenario across the coordinator's workers and
// builds its report the same way Run does for a single engine. The
// coordinator's result is returned too, for per-worker detail. When ctx
// is cancelled the run is stopped gracefully, like a local run.
//
// If no worker finished, the report (built from whatever arrived), the
// result and an error are all returned.
func RunDistributed(ctx context.Context, o DistributedOptions) (*report.Report, *coordinator.Result, error) {
	if o.Coordinator == nil {
		return nil, nil, errors.New("runner: a coordinator is required")
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	sc, err := scenario.Parse(o.Spec.Scenario)
	if err != nil {
		return nil, nil, err
	}
	prog, err := scenario.Compile(sc)
	if err != nil {
		return nil, nil, err
	}
	plan, err := sc.Load.Plan()
	if err != nil {
		return nil, nil, err
	}
	if plan.StopOnFail && len(prog.Thresholds) == 0 {
		return nil, nil, ErrNoTargets
	}

	run, err := o.Coordinator.Start(ctx, o.Spec)
	if err != nil {
		return nil, nil, err
	}
	if o.OnStart != nil {
		o.OnStart(run)
	}
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		select {
		case <-ctx.Done():
			run.Stop()
		case <-stopWatch:
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for ev := range run.Events() {
			if o.OnEvent != nil {
				o.OnEvent(ev)
			}
		}
	}()
	var bp *BreakpointTracker
	if plan.StopOnFail {
		bp = NewBreakpointTracker(prog, plan)
	}
	go func() {
		defer wg.Done()
		for s := range run.Snapshots() {
			if bp != nil && bp.Observe(s) {
				run.StopWithReason("breakpoint reached")
			}
			if o.Progress != nil {
				o.Progress(Progress{
					Elapsed: time.Duration(s.Interval+1) * run.Interval(), Total: plan.TotalDuration(),
					Snapshot: s, Planned: s.Planned, Mode: plan.Mode,
				})
			}
		}
	}()
	res, werr := run.Wait(context.Background())
	wg.Wait()
	if res == nil {
		return nil, nil, werr
	}

	in := report.Input{
		RunID: o.Spec.ID, Program: prog, Plan: plan, Target: renderTarget(sc, o.Spec.Env, o.Spec.Secrets),
		Started: res.T0, Ended: res.End, StopReason: res.StopReason, PeakVUs: res.PeakVUs,
		Workers: len(res.Workers), Interval: res.Interval, Snapshots: res.Snapshots, Phases: res.Phases,
	}
	if bp != nil {
		in.Breakpoint = bp.Result()
	}
	rep := report.Build(in)
	annotate(rep, res)
	return rep, res, werr
}

// annotate adds the per-worker table and explains losses, failures and
// saturation in the report's notes.
func annotate(rep *report.Report, res *coordinator.Result) {
	secs := res.Interval.Seconds()
	if secs <= 0 {
		secs = 1
	}
	span := func(w coordinator.Window) report.Span {
		return report.Span{From: float64(w.From) * secs, To: float64(w.To+1) * secs}
	}
	saturated := false
	for _, ws := range res.Workers {
		row := report.WorkerRow{
			ID: ws.ID, Name: ws.Name, Region: ws.Region, ShareLo: ws.ShareLo, ShareHi: ws.ShareHi,
			State: ws.State, StopReason: ws.StopReason, Error: ws.Error, PeakVUs: ws.PeakVUs,
			Requests: ws.Requests, SaturationReasons: ws.SaturationReasons, ClockOffset: ws.ClockOffset.Seconds(),
		}
		for _, w := range ws.Saturated {
			row.Saturated = append(row.Saturated, span(w))
		}
		if ws.Lost != nil {
			s := span(*ws.Lost)
			row.Lost = &s
		}
		rep.Workers = append(rep.Workers, row)

		share := 100 * (ws.ShareHi - ws.ShareLo)
		who := ws.Name
		if ws.Region != "" {
			who += " (" + ws.Region + ")"
		}
		switch ws.State {
		case coordinator.WorkerLost:
			rep.Notes = append(rep.Notes, fmt.Sprintf(
				"Worker %s was lost at %s; its %.1f%% share of the load was not reassigned, so from then on the run generated about %.1f%% less load than planned. Its data up to the loss is included.",
				who, fmtSecs(row.Lost.From), share, share))
		case coordinator.WorkerFailed:
			rep.Notes = append(rep.Notes, fmt.Sprintf("Worker %s failed (%s); its %.1f%% share of the load stopped there.", who, ws.Error, share))
		}
		if len(ws.Saturated) > 0 {
			saturated = true
			var total float64
			var parts []string
			for _, s := range row.Saturated {
				total += s.To - s.From
				parts = append(parts, fmtSecs(s.From)+"–"+fmtSecs(s.To))
			}
			rep.Notes = append(rep.Notes, fmt.Sprintf(
				"Worker %s was saturated for %s (%s: %s). Latency measured in those windows may reflect the load generator rather than the target; add workers or capacity.",
				who, fmtSecs(total), strings.Join(parts, ", "), strings.Join(ws.SaturationReasons, ", ")))
		}
	}
	sort.SliceStable(rep.Workers, func(i, j int) bool { return rep.Workers[i].ShareLo < rep.Workers[j].ShareLo })
	// A failed target measured by saturated workers says as much about
	// the generator as about the system under test.
	if saturated && rep.Verdict == report.VerdictFail {
		rep.Verdict = report.VerdictGeneratorLimit
		rep.Notes = append(rep.Notes, "Targets failed while workers were saturated, so the failure may be the generator's; the verdict is generator-limited rather than fail.")
	}
}

func fmtSecs(s float64) string {
	return time.Duration(s * float64(time.Second)).Round(time.Second).String()
}

// renderTarget renders target.baseURL for the report; it is blank when
// the URL depends on per-user data.
func renderTarget(sc *scenario.Scenario, env, secrets map[string]string) string {
	scope, err := scenario.NewScope()
	if err != nil {
		return ""
	}
	t, err := scope.CompileTemplate(sc.Target.BaseURL)
	if err != nil {
		return ""
	}
	if env == nil {
		env = map[string]string{}
	}
	if secrets == nil {
		secrets = map[string]string{}
	}
	out, err := t.Render(activation{"env": env, "secret": secrets, "vars": sc.Vars, "data": map[string]any{}})
	if err != nil {
		return ""
	}
	return out
}

type activation map[string]any

func (a activation) ResolveName(n string) (any, bool) { v, ok := a[n]; return v, ok }
func (a activation) Parent() interpreter.Activation   { return nil }
