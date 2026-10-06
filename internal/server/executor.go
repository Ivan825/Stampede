package server

import (
	"context"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/Ivan825/Stampede/internal/engine"
	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// ExecSpec is everything needed to execute a run.
type ExecSpec struct {
	RunID    string
	Scenario *scenario.Scenario
	// YAML is the final scenario (overrides applied, base URL set) for
	// executors that ship it to workers.
	YAML    []byte
	Env     map[string]string
	Secrets map[string]string
	// AllowHosts are public hosts besides the target that requests may reach.
	AllowHosts []string
	TargetHost string
	// Workers is how many workers to use (0 = all available).
	Workers int
}

// ExecEvent is something worth recording about a run: a worker joining,
// being lost or saturated, a safety stop.
type ExecEvent struct {
	Type    string
	Message string
	Worker  string
	Details map[string]any
}

// ExecResult describes a finished execution.
type ExecResult struct {
	T0         time.Time
	End        time.Time
	StopReason string
	Phases     map[int]*[metrics.NumPhases]*metrics.Histogram
	PeakVUs    int
	Workers    int
	Notes      []string
}

// Execution is a run in progress.
type Execution interface {
	// Snapshots delivers one merged snapshot per interval, in order, and
	// is closed when the run ends.
	Snapshots() <-chan *metrics.Snapshot
	// Events is closed when the run ends.
	Events() <-chan ExecEvent
	Stop(reason string)
	Kill()
	Wait(ctx context.Context) (*ExecResult, error)
}

// Executor starts executions.
type Executor interface {
	Start(ctx context.Context, spec ExecSpec) (Execution, error)
}

// WorkerInfo describes a connected worker for GET /workers.
type WorkerInfo struct {
	ID          string
	Name        string
	Region      string
	Version     string
	Labels      map[string]string
	CPUs        int
	MemoryBytes int64
	Status      string
	RunID       string
	ConnectedAt time.Time
	LastSeenAt  time.Time
}

// LocalExecutor runs load inside the server process. It suits a single
// machine and development; use distributed workers for real load so the
// control plane and the generator do not compete for CPU.
type LocalExecutor struct {
	Logger *slog.Logger
}

type localExec struct {
	eng    *engine.Engine
	snaps  chan *metrics.Snapshot
	events chan ExecEvent
	done   chan struct{}
	res    *engine.Result
	err    error
}

// Start compiles the scenario and runs it on an in-process engine.
func (l *LocalExecutor) Start(ctx context.Context, spec ExecSpec) (Execution, error) {
	prog, err := scenario.Compile(spec.Scenario)
	if err != nil {
		return nil, err
	}
	plan, err := spec.Scenario.Load.Plan()
	if err != nil {
		return nil, err
	}
	policy := safety.NewHostPolicy(spec.TargetHost, spec.AllowHosts)
	x := &localExec{
		snaps:  make(chan *metrics.Snapshot, 256),
		events: make(chan ExecEvent, 16),
		done:   make(chan struct{}),
	}
	log := l.Logger
	if log == nil {
		log = slog.Default()
	}
	x.eng, err = engine.New(engine.Options{
		Program: prog, Plan: plan, RunID: spec.RunID,
		Env: spec.Env, Secrets: spec.Secrets,
		AllowHost:  func(u *url.URL) bool { return policy.Allow(u) },
		OnSnapshot: func(s *metrics.Snapshot) { x.snaps <- s },
		Logger:     log.With("run", spec.RunID),
	})
	if err != nil {
		return nil, err
	}
	x.events <- ExecEvent{Type: "worker.started", Message: "load generator started inside the server", Worker: "local"}
	var once sync.Once
	go func() {
		x.res, x.err = x.eng.Run(context.WithoutCancel(ctx))
		once.Do(func() {
			close(x.snaps)
			close(x.events)
			close(x.done)
		})
	}()
	return x, nil
}

func (x *localExec) Snapshots() <-chan *metrics.Snapshot { return x.snaps }
func (x *localExec) Events() <-chan ExecEvent            { return x.events }
func (x *localExec) Stop(reason string)                  { x.eng.Stop(reason) }
func (x *localExec) Kill()                               { x.eng.Kill() }

func (x *localExec) Wait(ctx context.Context) (*ExecResult, error) {
	select {
	case <-x.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if x.err != nil {
		return nil, x.err
	}
	return &ExecResult{
		T0: x.res.T0, End: x.res.End, StopReason: x.res.StopReason,
		Phases: x.res.Phases, PeakVUs: x.res.PeakVUs, Workers: 1,
	}, nil
}
