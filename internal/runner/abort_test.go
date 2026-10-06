package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/scenario"
)

func snap(reqs, failed int, lat time.Duration) *metrics.Snapshot {
	s := metrics.NewSnapshot(0)
	t0 := time.Unix(0, 0)
	for i := 0; i < reqs; i++ {
		s.Step(0).Add(&metrics.Sample{Start: t0, End: t0.Add(lat), Failed: i < failed})
	}
	return s
}

func TestAbortOnSustainedErrors(t *testing.T) {
	half := scenario.Percent(0.5)
	w := NewAbortWatcher(&scenario.Abort{Errors: &half, For: scenario.Duration(3 * time.Second)}, nil, time.Second)
	seq := []*metrics.Snapshot{snap(10, 9, time.Millisecond), snap(10, 9, time.Millisecond), snap(10, 1, time.Millisecond),
		snap(10, 6, time.Millisecond), snap(0, 0, 0), snap(10, 10, time.Millisecond)}
	for i, s := range seq {
		if r := w.Observe(s); r != "" {
			t.Fatalf("tripped too early at %d: %s", i, r)
		}
	}
	if r := w.Observe(snap(10, 5, time.Millisecond)); r == "" {
		t.Fatal("three bad intervals in a row (ignoring empty ones) should trip")
	}
}

func TestAbortOnLatencyAndFloor(t *testing.T) {
	floor := &scenario.Abort{P95: scenario.Duration(time.Second), For: scenario.Duration(2 * time.Second)}
	w := NewAbortWatcher(nil, floor, time.Second)
	if w.Observe(snap(5, 0, 2*time.Second)) != "" {
		t.Fatal("one slow interval is not enough")
	}
	if r := w.Observe(snap(5, 0, 2*time.Second)); r == "" {
		t.Fatal("the server floor should apply when the scenario sets nothing")
	}
	if NewAbortWatcher(nil, nil, time.Second) != nil {
		t.Error("no limits means no watcher")
	}
}

func TestRunAbortsOnFailingTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	s, err := scenario.Parse([]byte(fmt.Sprintf(`
metadata: {name: abort}
target: {baseURL: %q}
journeys: [{name: a, steps: [{get: /}]}]
load: {mode: rate, rate: 50/s, duration: 30s, abort: {errors: 50%%, for: 2s}}`, srv.URL)))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	rep, err := Run(context.Background(), Options{Scenario: s, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("run took %s; it should have aborted after about 2s", took)
	}
	if !strings.HasPrefix(rep.StopReason, "aborted: errors") {
		t.Errorf("stop reason %q", rep.StopReason)
	}
}
