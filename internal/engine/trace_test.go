package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
)

// TestSlowestCarryTraceIDs checks the slowest requests keep the trace ID
// the target received in traceparent, so a report can link to the trace.
func TestSlowestCarryTraceIDs(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.Header.Get("traceparent"), "-")
		if len(parts) == 4 {
			mu.Lock()
			seen[parts[1]] = true
			mu.Unlock()
		}
		if n.Add(1)%7 == 0 {
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer srv.Close()
	out := run(t, fmt.Sprintf(`
metadata: {name: traced}
target: {baseURL: %q}
journeys: [{name: a, steps: [{get: /x}]}]
load: {vus: 2, duration: 1s, gracefulStop: 2s}`, srv.URL), nil)
	var slow []metrics.SlowRequest
	for _, st := range out.total.Steps {
		slow = append(slow, st.Slowest...)
	}
	if len(slow) != metrics.MaxSlowest {
		t.Fatalf("kept %d slow requests, want %d", len(slow), metrics.MaxSlowest)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, s := range slow {
		if !seen[s.TraceID] {
			t.Errorf("slow request trace %q was never sent to the target", s.TraceID)
		}
		if s.Latency < 20*time.Millisecond {
			t.Errorf("slow request latency %v, want the 20ms responses", s.Latency)
		}
	}
}
