package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/runner"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// Past the planned duration, users finish their journeys; the line says so
// instead of counting past the plan.
func TestProgressAfterThePlanSaysFinishing(t *testing.T) {
	var b bytes.Buffer
	p := progressPrinter(&b)
	p(runner.Progress{Elapsed: 12 * time.Second, Total: 20 * time.Second, Snapshot: &metrics.Snapshot{}, Planned: 1000, Mode: scenario.ModeVUs})
	p(runner.Progress{Elapsed: 32 * time.Second, Total: 20 * time.Second, Snapshot: &metrics.Snapshot{}, Planned: 1000, Mode: scenario.ModeVUs})
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if !strings.Contains(lines[0], "12s/20s") || !strings.Contains(lines[0], "plan 1000 VUs") {
		t.Errorf("during the plan: %q", lines[0])
	}
	if !strings.Contains(lines[1], "20s/20s") || !strings.Contains(lines[1], "finishing") || strings.Contains(lines[1], "32s") {
		t.Errorf("after the plan: %q", lines[1])
	}
}

func TestProgressWithoutRequestsShowsNoLatency(t *testing.T) {
	var b bytes.Buffer
	progressPrinter(&b)(runner.Progress{Elapsed: 2 * time.Second, Total: 20 * time.Second, Snapshot: &metrics.Snapshot{}, Planned: 10, Mode: scenario.ModeVUs})
	if !strings.Contains(b.String(), "p95 -") || strings.Contains(b.String(), "0.00ms") {
		t.Errorf("progress line: %q", b.String())
	}
}
