package pack

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"sort"
	"strings"

	"github.com/Ivan825/Stampede/internal/runner"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// JourneyResult is the outcome of running one journey once.
type JourneyResult struct {
	File    string
	Journey string
	OK      bool
	// Problem describes the first failure.
	Problem string
}

// DryRun runs every journey in a scenario exactly once with one user and
// reports which ones work end to end against the target.
func DryRun(ctx context.Context, file string, s *scenario.Scenario, env map[string]string, allow func(*url.URL) bool) ([]JourneyResult, error) {
	var out []JourneyResult
	for _, j := range s.Journeys {
		one := *s
		one.Journeys = []scenario.Journey{j}
		one.Journeys[0].Weight = 1
		one.Load = scenario.Load{Mode: scenario.ModeVUs, Iterations: 1, VUs: 1}
		one.Targets = nil
		data := map[string]scenario.Feeder{}
		for k, f := range s.Data {
			// A dry run must not exhaust unique data meant for the real run.
			if f.Mode == scenario.FeedUnique {
				f.Mode = scenario.FeedSequential
			}
			data[k] = f
		}
		one.Data = data
		if err := one.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		rep, err := runner.Run(ctx, runner.Options{
			Scenario: &one, Env: env, Secrets: env, AllowHost: allow,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			return nil, fmt.Errorf("%s / %s: %w", file, j.Name, err)
		}
		r := JourneyResult{File: file, Journey: j.Name, OK: rep.Overall.Failed == 0 && rep.Overall.Iterations == 1 && rep.Overall.IterationsFailed == 0}
		if !r.OK {
			var probs []string
			for _, e := range rep.Errors {
				probs = append(probs, fmt.Sprintf("%s at %s", e.Error, e.Step))
			}
			sort.Strings(probs)
			r.Problem = strings.Join(probs, "; ")
			if r.Problem == "" {
				r.Problem = "the journey did not complete"
			}
		}
		out = append(out, r)
	}
	return out, nil
}
