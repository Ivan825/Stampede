package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Ivan825/Stampede/internal/agent"
	"github.com/Ivan825/Stampede/internal/protocol/netem"
	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// FaultPlan is a scenario's fault timeline with a client for the agent
// that injects it.
type FaultPlan struct {
	Client *agent.Client
	Steps  []scenario.FaultStep
}

// request turns a step into an agent request.
func request(f scenario.FaultStep, runID string) (agent.Request, error) {
	req := agent.Request{Duration: f.For.D(), Run: runID, Label: f.Label()}
	switch {
	case f.Proxy != "":
		req.Kind, req.Target = agent.KindProxy, f.Proxy
		req.Fault = agent.Fault{Latency: f.Latency.D(), Jitter: f.Jitter.D(), Reset: f.Reset, Refuse: f.Refuse, Blackhole: f.Blackhole}
		if f.Bandwidth != "" {
			bw, err := netem.ParseBandwidth(f.Bandwidth)
			if err != nil {
				return req, err
			}
			req.Fault.Bandwidth = bw
		}
	case f.Container != "":
		req.Kind, req.Target, req.Action = agent.KindContainer, f.Container, f.Action
	default:
		req.Kind, req.Target, req.Replicas = agent.KindDeployment, f.Deployment, f.Replicas
	}
	return req, nil
}

// Check asks the agent whether it can carry out every step, so a run with
// an unreachable agent, a missing proxy or a missing permission fails
// before any load is sent.
func (p *FaultPlan) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	st, err := p.Client.Status(ctx)
	if err != nil {
		return fmt.Errorf("faults: %w", err)
	}
	proxies := map[string]bool{}
	var names []string
	for _, px := range st.Proxies {
		proxies[px.Name] = true
		names = append(names, px.Name)
	}
	maxDur, _ := time.ParseDuration(st.MaxDuration)
	var problems []string
	for i, f := range p.Steps {
		where := fmt.Sprintf("faults.timeline[%d] (%s)", i, f.Label())
		switch {
		case f.Proxy != "" && !proxies[f.Proxy]:
			problems = append(problems, fmt.Sprintf("%s: the agent has no proxy %q (it has: %s)", where, f.Proxy, strings.Join(names, ", ")))
		case f.Container != "" && !st.Docker:
			problems = append(problems, where+": the agent was not started with --allow-container")
		case f.Deployment != "" && !st.Kubernetes:
			problems = append(problems, where+": the agent was not started with --allow-deployment")
		}
		if maxDur > 0 && f.For.D() > maxDur {
			problems = append(problems, fmt.Sprintf("%s: lasts %s, over the agent's --max-duration of %s", where, f.For.D(), maxDur))
		}
	}
	if len(problems) > 0 {
		return errors.New("faults: " + strings.Join(problems, "; "))
	}
	return nil
}

// FaultRun applies a plan's steps on time during one run.
type FaultRun struct {
	plan   *FaultPlan
	runID  string
	t0     time.Time
	log    *slog.Logger
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	events []report.FaultEvent
}

// Start applies each step at t0 plus its offset. t0 may be in the future
// (a distributed run starts a few seconds after it is scheduled).
func (p *FaultPlan) Start(ctx context.Context, t0 time.Time, runID string, log *slog.Logger) *FaultRun {
	ctx, cancel := context.WithCancel(ctx)
	fr := &FaultRun{plan: p, runID: runID, t0: t0, log: log, cancel: cancel}
	for _, step := range p.Steps {
		fr.wg.Add(1)
		go func() {
			defer fr.wg.Done()
			select {
			case <-time.After(time.Until(t0.Add(step.At.D()))):
			case <-ctx.Done():
				return
			}
			fr.apply(ctx, step)
		}()
	}
	return fr
}

func (fr *FaultRun) apply(ctx context.Context, step scenario.FaultStep) {
	req, err := request(step, fr.runID)
	ev := report.FaultEvent{Label: step.Label(), Kind: string(req.Kind), Target: req.Target}
	at := time.Since(fr.t0).Seconds()
	ev.Start, ev.End = at, at+step.For.D().Seconds()
	if err == nil {
		_, err = fr.plan.Client.Apply(ctx, req)
	}
	if err != nil {
		ev.Error = err.Error()
		fr.log.Warn("fault not applied", "fault", ev.Label, "error", err)
	} else {
		fr.log.Info("fault applied", "fault", ev.Label, "for", step.For.D())
	}
	fr.mu.Lock()
	fr.events = append(fr.events, ev)
	fr.mu.Unlock()
}

// Stop cancels faults not yet started, clears every fault of the run on
// the agent and returns what happened, in start order. Faults that would
// have outlasted the run end when it ends.
func (fr *FaultRun) Stop(end time.Time) []report.FaultEvent {
	fr.cancel()
	fr.wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	revertErr := fr.plan.Client.RevertAll(ctx, fr.runID)
	endAt := end.Sub(fr.t0).Seconds()
	fr.mu.Lock()
	defer fr.mu.Unlock()
	for i := range fr.events {
		e := &fr.events[i]
		if e.End > endAt {
			e.End = endAt
			if revertErr != nil && e.Error == "" {
				e.Error = "could not be cleared at the end of the run: " + revertErr.Error() + " (the agent reverts it when its duration ends)"
			}
		}
	}
	if revertErr != nil {
		fr.log.Error("could not clear the run's faults on the agent", "error", revertErr)
	}
	out := append([]report.FaultEvent(nil), fr.events...)
	sortEvents(out)
	return out
}

func sortEvents(es []report.FaultEvent) {
	for i := 1; i < len(es); i++ {
		for j := i; j > 0 && es[j].Start < es[j-1].Start; j-- {
			es[j], es[j-1] = es[j-1], es[j]
		}
	}
}
