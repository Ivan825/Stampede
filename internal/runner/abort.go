package runner

import (
	"fmt"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// AbortWatcher trips when every interval in a sliding window exceeds a
// limit: the target has been failing for that long, not just for a blip.
// Intervals with no requests neither trip nor reset it.
type AbortWatcher struct {
	errors   float64 // ratio, 0 = no limit
	p95      time.Duration
	window   int
	errRun   int
	latRun   int
	tripped  string
	interval time.Duration
}

// NewAbortWatcher builds a watcher from a scenario's abort block, falling
// back to floor (the server's default) for limits the scenario leaves out.
// It returns nil when no limit applies.
func NewAbortWatcher(a *scenario.Abort, floor *scenario.Abort, interval time.Duration) *AbortWatcher {
	if interval <= 0 {
		interval = time.Second
	}
	w := &AbortWatcher{interval: interval}
	for _, src := range []*scenario.Abort{a, floor} {
		if src == nil {
			continue
		}
		if w.errors == 0 && src.Errors != nil {
			w.errors = float64(*src.Errors)
		}
		if w.p95 == 0 && src.P95 > 0 {
			w.p95 = src.P95.D()
		}
		if w.window == 0 && src.For > 0 {
			w.window = int((src.For.D() + interval - 1) / interval)
		}
	}
	if w.errors == 0 && w.p95 == 0 {
		return nil
	}
	if w.window == 0 {
		w.window = int(10 * time.Second / interval)
	}
	return w
}

// Observe adds an interval and returns a stop reason once the abort trips.
func (w *AbortWatcher) Observe(s *metrics.Snapshot) string {
	if w == nil || w.tripped != "" {
		return w.reasonOrEmpty()
	}
	t := s.Totals()
	if t.Requests == 0 {
		return ""
	}
	if w.errors > 0 {
		if rate := float64(t.Failed) / float64(t.Requests); rate >= w.errors {
			w.errRun++
		} else {
			w.errRun = 0
		}
		if w.errRun >= w.window {
			w.tripped = fmt.Sprintf("aborted: errors at or above %s for %s", report.Pct(w.errors), time.Duration(w.window)*w.interval)
		}
	}
	if w.p95 > 0 && w.tripped == "" {
		if p := time.Duration(t.Latency.Quantile(0.95)) * time.Microsecond; p >= w.p95 {
			w.latRun++
		} else {
			w.latRun = 0
		}
		if w.latRun >= w.window {
			w.tripped = fmt.Sprintf("aborted: p95 at or above %s for %s", w.p95, time.Duration(w.window)*w.interval)
		}
	}
	return w.tripped
}

func (w *AbortWatcher) reasonOrEmpty() string {
	if w == nil {
		return ""
	}
	return w.tripped
}
