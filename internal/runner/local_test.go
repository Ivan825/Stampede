package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/scenario"
)

func TestBreakpointFindsLimit(t *testing.T) {
	// The server is fast until more than 4 requests are in flight, then
	// every request takes 150ms: a hard concurrency limit.
	var inflight atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inflight.Add(1)
		defer inflight.Add(-1)
		time.Sleep(20 * time.Millisecond)
		if n > 4 {
			time.Sleep(150 * time.Millisecond)
		}
	}))
	defer srv.Close()
	s, err := scenario.Parse([]byte(fmt.Sprintf(`
metadata: {name: bp}
target: {baseURL: %q}
journeys: [{name: a, steps: [{get: /}]}]
load: {shape: breakpoint, mode: rate, start: 20/s, max: 400/s, steps: 6, stepDuration: 2s}
targets: ["p95 < 100ms"]`, srv.URL)))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), Options{Scenario: s, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	bp := rep.Breakpoint
	if bp == nil || !bp.Found {
		t.Fatalf("breakpoint not found: %+v", bp)
	}
	// ~4 in flight at 20ms each is ~200/s; it must pass low levels and fail by 400/s.
	if bp.LastPass < 20 || bp.FirstFail <= bp.LastPass || bp.FirstFail > 400 {
		t.Errorf("unexpected breakpoint %+v", bp)
	}
	if rep.StopReason != "breakpoint reached" {
		t.Errorf("stop reason %q", rep.StopReason)
	}
}

func TestBreakpointNeedsTargets(t *testing.T) {
	s, _ := scenario.Parse([]byte(`
metadata: {name: bp}
target: {baseURL: "http://127.0.0.1:1"}
journeys: [{name: a, steps: [{get: /}]}]
load: {shape: breakpoint, max: '10'}`))
	if _, err := Run(context.Background(), Options{Scenario: s}); err != ErrNoTargets {
		t.Errorf("got %v", err)
	}
}
