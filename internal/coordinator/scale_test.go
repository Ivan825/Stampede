package coordinator_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/worker"
)

// TestCoordinates200Workers registers 200 real in-process workers (each
// with its own gRPC connection and engine), runs a short rate scenario
// across all of them and checks the merged result is exact.
func TestCoordinates200Workers(t *testing.T) {
	n := 200
	if runtime.GOOS == "darwin" && os.Getenv("CI") != "" {
		// Hosted macOS runners have a fraction of the Linux runners' CPU;
		// 200 in-process workers under the race detector overload them.
		// The Linux job proves 200; this one checks the same paths.
		n = 60
	}
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()

	c := newCluster(t, coordinator.Config{})
	begin := time.Now()
	for i := 0; i < n; i++ {
		c.addWorker(worker.Config{Name: fmt.Sprintf("w%03d", i), CPUs: 1 + i%4})
	}
	c.waitConnected(n, 30*time.Second)
	registered := time.Since(begin)

	// The engine skips an arrival it cannot dispatch before the plan's
	// end; at 400/s the last one is due 2.5ms before it, which 200 engines
	// under the race detector on a busy machine occasionally miss. 100/s
	// leaves 10ms, so the check stays exact without being flaky.
	const rate, secs = 100, 3
	startAt := time.Now()
	r, err := c.coord.Start(context.Background(), coordinator.RunSpec{
		ID: "scale", Scenario: rateScenario(srv.URL, rate, secs), StartDelay: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted := time.Since(startAt)
	peakGoroutines := runtime.NumGoroutine()
	out := collect(t, r, 60*time.Second)
	if out.err != nil {
		t.Fatal(out.err)
	}
	res := out.res
	if len(res.Workers) != n {
		t.Fatalf("%d workers took part, want %d", len(res.Workers), n)
	}
	var exact int
	for _, ws := range res.Workers {
		if ws.State != coordinator.WorkerFinished {
			t.Errorf("%s: %s %s", ws.Name, ws.State, ws.Error)
		}
		if ws.Iterations == expectedArrivals(rate*secs, ws.ShareLo, ws.ShareHi) {
			exact++
		}
	}
	if exact != n {
		t.Errorf("%d of %d workers ran exactly their share", exact, n)
	}
	if got := started(res.Snapshots); got != rate*secs {
		t.Errorf("merged %d iterations, want exactly %d", got, rate*secs)
	}
	if hits.Load() != rate*secs {
		t.Errorf("server saw %d requests", hits.Load())
	}
	// The merged result above is exact. Live, a starved runner may emit
	// an interval before one of 200 workers' snapshots arrives (it is
	// merged in later); more than one such interval would mean the
	// coordinator cannot keep up.
	if inc := len(out.eventsOf(coordinator.EventIntervalIncomplete)); res.Degraded || inc > 1 {
		t.Errorf("degraded %v, incomplete intervals %d", res.Degraded, inc)
	}
	var worstRTT time.Duration
	for _, ws := range res.Workers {
		worstRTT = max(worstRTT, ws.ClockRTT)
	}
	t.Logf("%d workers: registered in %v, clock-synced and accepted in %v, run took %v after T0; %d goroutines at start; worst clock RTT %v",
		n, registered.Round(time.Millisecond), accepted.Round(time.Millisecond), time.Since(r.T0()).Round(time.Millisecond), peakGoroutines, worstRTT)
}
