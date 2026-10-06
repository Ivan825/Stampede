// Package runtest runs a scenario file on an in-process engine, for
// end-to-end tests of plugins and their example scenarios.
package runtest

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Ivan825/Stampede/internal/engine"
	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// Result is a finished run's merged metrics.
type Result struct {
	Program *scenario.Program
	Total   *metrics.Snapshot
}

// Step returns the stats of the step with the given name.
func (r *Result) Step(t testing.TB, name string) *metrics.StepStats {
	t.Helper()
	for _, st := range r.Program.Steps {
		if st.Name == name {
			if s := r.Total.Steps[st.ID]; s != nil {
				return s
			}
			return &metrics.StepStats{}
		}
	}
	t.Fatalf("no step %q", name)
	return nil
}

// Run runs the scenario file with plugins from pluginDir and env as
// ${env.X}. load, when set, replaces the scenario's load section.
func Run(t testing.TB, file, pluginDir string, env map[string]string, load *scenario.Load) *Result {
	t.Helper()
	s, err := scenario.LoadFile(file)
	if err != nil {
		t.Fatalf("%s: %v", filepath.Base(file), err)
	}
	if load != nil {
		s.Load = *load
		if s.Load.Mode == "" {
			s.Load.Mode = scenario.ModeVUs
		}
	}
	prog, err := scenario.Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.Load.Plan()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	total := metrics.NewSnapshot(0)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("STAMPEDE_TEST_LOG") != "" {
		log = slog.Default()
	}
	e, err := engine.New(engine.Options{
		Program: prog, Plan: plan, RunID: "runtest", Env: env, PluginDir: pluginDir, Logger: log,
		OnSnapshot: func(sn *metrics.Snapshot) {
			mu.Lock()
			total.Merge(sn)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &Result{Program: prog, Total: total}
}
