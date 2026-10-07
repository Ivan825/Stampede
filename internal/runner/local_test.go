package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/report"
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
	// Confirmation holds narrow the step-sized gap (76/s here) by halving.
	if len(bp.Refined) == 0 || bp.FirstFail-bp.LastPass > 76/2+1 {
		t.Errorf("not narrowed: %+v", bp)
	}
	for _, st := range bp.Refined {
		if st.Pass && st.Level > bp.LastPass || !st.Pass && st.Level < bp.FirstFail {
			t.Errorf("round %+v outside the final bracket [%v, %v]", st, bp.LastPass, bp.FirstFail)
		}
	}
	t.Logf("breakpoint between %v and %v after %d rounds: %+v", bp.LastPass, bp.FirstFail, len(bp.Refined), bp.Refined)
}

func TestRefineBisects(t *testing.T) {
	bp := &report.Breakpoint{Found: true, LastPass: 100, FirstFail: 200, FailedOn: []string{"p95 < 1s"}}
	var tried []float64
	err := Refine(context.Background(), bp, scenario.ModeRate, 3, func(_ context.Context, level float64) (bool, []string, error) {
		tried = append(tried, level)
		if level <= 160 {
			return true, nil, nil
		}
		return false, []string{"errors < 1%"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// 150 holds, 175 fails, 162.5 fails.
	if fmt.Sprint(tried) != "[150 175 162.5]" || bp.LastPass != 150 || bp.FirstFail != 162.5 || bp.FailedOn[0] != "errors < 1%" {
		t.Fatalf("tried %v, bracket %v-%v %v", tried, bp.LastPass, bp.FirstFail, bp.FailedOn)
	}
	// Whole users: a gap of one cannot be split.
	bp = &report.Breakpoint{Found: true, LastPass: 10, FirstFail: 11}
	calls := 0
	_ = Refine(context.Background(), bp, scenario.ModeVUs, 3, func(context.Context, float64) (bool, []string, error) { calls++; return true, nil, nil })
	if calls != 0 {
		t.Errorf("%d rounds for a gap of one user", calls)
	}
}

func TestBreakpointNeedsTargets(t *testing.T) {
	s, _ := scenario.Parse([]byte(`
metadata: {name: bp}
target: {baseURL: "http://127.0.0.1:1"}
journeys: [{name: a, steps: [{get: /}]}]
load: {shape: breakpoint, max: '10'}`))
	if _, err := Run(context.Background(), Options{Scenario: s}); !errors.Is(err, ErrNoTargets) {
		t.Errorf("got %v", err)
	}
}
