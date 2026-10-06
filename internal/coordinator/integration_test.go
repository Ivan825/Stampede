package coordinator_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/worker"
)

func TestDistributedRateMatchesSingleEngine(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()

	c := newCluster(t, coordinator.Config{})
	// Unequal capacity: the shares are 1/6, 2/6 and 3/6.
	for i, cpus := range []int{1, 2, 3} {
		c.addWorker(worker.Config{Name: string(rune('a' + i)), CPUs: cpus})
	}
	c.waitConnected(3, 5*time.Second)

	const rate, secs = 300, 2
	r, err := c.coord.Start(context.Background(), coordinator.RunSpec{
		ID: "rate", Scenario: rateScenario(srv.URL, rate, secs), StartDelay: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := collect(t, r, 20*time.Second)
	if out.err != nil {
		t.Fatal(out.err)
	}
	res := out.res

	// A single engine runs exactly rate*duration iterations (see the
	// engine's TestOpenModelRateIsExact); the shares must add up to the
	// same number, not approximately but exactly.
	const want = rate * secs
	if got := started(res.Snapshots); got != want {
		t.Errorf("merged result started %d iterations, want exactly %d", got, want)
	}
	if got := started(out.live); got != want {
		t.Errorf("live stream started %d iterations, want exactly %d", got, want)
	}
	if hits.Load() != want {
		t.Errorf("server saw %d requests, want %d", hits.Load(), want)
	}
	for _, ws := range res.Workers {
		if ws.State != coordinator.WorkerFinished || ws.StopReason != "completed" {
			t.Errorf("%s: state %s reason %q", ws.Name, ws.State, ws.StopReason)
		}
		if exp := expectedArrivals(want, ws.ShareLo, ws.ShareHi); ws.Iterations != exp {
			t.Errorf("%s share [%.3f, %.3f) ran %d iterations, want exactly %d", ws.Name, ws.ShareLo, ws.ShareHi, ws.Iterations, exp)
		}
	}
	// Share sizes follow CPUs.
	sizes := map[string]float64{}
	for _, ws := range res.Workers {
		sizes[ws.Name] = ws.ShareHi - ws.ShareLo
	}
	if d := sizes["c"] - 0.5; d < -1e-9 || d > 1e-9 {
		t.Errorf("3-CPU worker share %v, want 0.5", sizes["c"])
	}
	// Live snapshots arrive once per interval, in order, never duplicated.
	for i, s := range out.live {
		if s.Interval != int64(i) {
			t.Fatalf("live snapshot %d has interval %d", i, s.Interval)
		}
	}
	if res.StopReason != "completed" || res.Degraded || len(out.eventsOf(coordinator.EventIntervalIncomplete)) != 0 {
		t.Errorf("reason %q degraded %v incomplete %v", res.StopReason, res.Degraded, out.eventsOf(coordinator.EventIntervalIncomplete))
	}
	if len(res.Phases) == 0 {
		t.Error("no phase histograms merged")
	}
	if n := len(out.eventsOf(coordinator.EventWorkerJoined)); n != 3 {
		t.Errorf("%d joined events", n)
	}
	// Workers are released for the next run.
	for _, w := range c.coord.Workers() {
		if w.CurrentRun != "" {
			t.Errorf("worker %s still on run %s", w.Name, w.CurrentRun)
		}
	}
}

func TestClockSyncStartsWorkersTogether(t *testing.T) {
	var (
		mu       sync.Mutex
		arrivals []time.Time
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()

	c := newCluster(t, coordinator.Config{})
	// 3ms each way on the control connection.
	c.delay = 3 * time.Millisecond
	offsets := []time.Duration{7 * time.Second, -4 * time.Second, 150 * time.Millisecond}
	var startMu sync.Mutex
	starts := map[string]time.Time{}
	for i, off := range offsets {
		name := string(rune('a' + i))
		c.addWorker(worker.Config{
			Name:  name,
			Clock: func() time.Time { return time.Now().Add(off) },
			OnRunStart: func(_ string, t0 time.Time) {
				startMu.Lock()
				starts[name] = t0
				startMu.Unlock()
			},
		})
	}
	c.waitConnected(3, 5*time.Second)

	r, err := c.coord.Start(context.Background(), coordinator.RunSpec{
		ID: "clock", StartDelay: time.Second, Scenario: []byte(`
metadata: {name: clock}
target: {baseURL: "` + srv.URL + `"}
journeys: [{name: a, steps: [{get: /}]}]
load: {vus: 3, duration: 1s, gracefulStop: 2s}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	out := collect(t, r, 20*time.Second)
	if out.err != nil {
		t.Fatal(out.err)
	}

	// Each worker's T0, translated from its own skewed clock, lands on the
	// server's T0.
	var worst time.Duration
	for name, t0 := range starts {
		skew := t0.Sub(r.T0()).Abs()
		worst = max(worst, skew)
		if skew > 10*time.Millisecond {
			t.Errorf("worker %s starts %v away from T0", name, skew)
		}
	}
	if len(starts) != 3 {
		t.Fatalf("%d workers reported a start", len(starts))
	}
	for i, ws := range out.res.Workers {
		want := offsets[strings.IndexByte("abc", ws.Name[0])]
		if d := (ws.ClockOffset - want).Abs(); d > 2*time.Millisecond {
			t.Errorf("worker %d offset estimated %v, true %v", i, ws.ClockOffset, want)
		}
	}

	// End to end: each worker's one user sends its first request at T0,
	// so the first three requests the target sees are tightly grouped.
	mu.Lock()
	first := append([]time.Time(nil), arrivals...)
	mu.Unlock()
	sort.Slice(first, func(i, j int) bool { return first[i].Before(first[j]) })
	if len(first) < 3 {
		t.Fatalf("only %d requests", len(first))
	}
	spread := first[2].Sub(first[0])
	lead := first[0].Sub(r.T0())
	t.Logf("clock offsets %v with 3ms one-way latency: worst T0 skew %v, first-request spread %v, first request %v after T0",
		offsets, worst, spread, lead)
	if spread > 10*time.Millisecond {
		t.Errorf("first requests from the three workers are %v apart, want within 10ms", spread)
	}
	if lead < -time.Millisecond || lead > 50*time.Millisecond {
		t.Errorf("first request %v after T0", lead)
	}
}

func TestWorkerLossMidRunIsMarked(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()

	hb := 200 * time.Millisecond
	c := newCluster(t, coordinator.Config{HeartbeatInterval: hb})
	var victim *testWorker
	for i := 0; i < 3; i++ {
		tw := c.addWorker(worker.Config{Name: string(rune('a' + i)), CPUs: 1})
		if i == 1 {
			victim = tw
		}
	}
	c.waitConnected(3, 5*time.Second)

	const rate, secs = 200, 4
	r, err := c.coord.Start(context.Background(), coordinator.RunSpec{
		ID: "loss", Scenario: rateScenario(srv.URL, rate, secs), StartDelay: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Kill worker b's process 1.5s into the run.
	time.AfterFunc(time.Until(r.T0().Add(1500*time.Millisecond)), victim.cancel)
	out := collect(t, r, 30*time.Second)
	if out.err != nil {
		t.Fatal(out.err)
	}
	res := out.res
	if !res.Degraded || res.StopReason != "completed" {
		t.Errorf("degraded %v reason %q", res.Degraded, res.StopReason)
	}

	var sum, lostKept, lostShare uint64
	for _, ws := range res.Workers {
		sum += ws.Iterations
		exp := expectedArrivals(rate*secs, ws.ShareLo, ws.ShareHi)
		if ws.Name == "b" {
			lostKept, lostShare = ws.Iterations, exp
			if ws.State != coordinator.WorkerLost || ws.Lost == nil {
				t.Fatalf("b: state %s lost %v", ws.State, ws.Lost)
			}
			// Killed after 1.5s: intervals 0 and part of 1 reached the
			// server, nothing after.
			if ws.Lost.From != 1 || ws.Lost.To < 3 {
				t.Errorf("lost window %+v, want from interval 1 to the end", *ws.Lost)
			}
			if ws.Iterations == 0 || ws.Iterations >= exp/2 {
				t.Errorf("lost worker kept %d of %d iterations", ws.Iterations, exp)
			}
			continue
		}
		// The survivors' shares are untouched by the loss: exact.
		if ws.State != coordinator.WorkerFinished || ws.Iterations != exp {
			t.Errorf("%s: state %s, %d iterations, want exactly %d", ws.Name, ws.State, ws.Iterations, exp)
		}
	}
	// Data before the loss is kept and nothing is counted twice.
	if got := started(res.Snapshots); got != sum {
		t.Errorf("merged %d iterations, workers account for %d", got, sum)
	}
	if lost := out.eventsOf(coordinator.EventWorkerLost); len(lost) != 1 || lost[0].WorkerName != "b" {
		t.Errorf("lost events %+v", lost)
	}
	if deg := out.eventsOf(coordinator.EventDegraded); len(deg) != 1 || !strings.Contains(deg[0].Message, "66.7% of plan") {
		t.Errorf("degraded events %+v", deg)
	}
	t.Logf("lost worker's data kept: %d of its %d iterations (killed 1.5s into 4s); survivors exact; merged %d of %d planned",
		lostKept, lostShare, sum, rate*secs)
}

func TestKillReachesWorkersWithinOneSecond(t *testing.T) {
	var inflight, total atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inflight.Add(1)
		total.Add(1)
		defer inflight.Add(-1)
		select {
		case <-r.Context().Done():
		case <-time.After(20 * time.Second):
		}
	}))
	defer srv.Close()

	c := newCluster(t, coordinator.Config{})
	var killMu sync.Mutex
	killed := map[string]time.Time{}
	for i := 0; i < 3; i++ {
		name := string(rune('a' + i))
		c.addWorker(worker.Config{Name: name, OnStop: func(_ string, kill bool, _ string) {
			if kill {
				killMu.Lock()
				killed[name] = time.Now()
				killMu.Unlock()
			}
		}})
	}
	c.waitConnected(3, 5*time.Second)

	r, err := c.coord.Start(context.Background(), coordinator.RunSpec{
		ID: "kill", StartDelay: 300 * time.Millisecond, Scenario: []byte(`
metadata: {name: kill}
target: {baseURL: "` + srv.URL + `"}
journeys: [{name: a, steps: [{get: /}]}]
load: {vus: 12, duration: 1m, gracefulStop: 30s}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for inflight.Load() < 12 {
		if time.Now().After(deadline) {
			t.Fatalf("only %d requests in flight", inflight.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}

	killAt := time.Now()
	r.Kill()
	for inflight.Load() > 0 {
		if time.Since(killAt) > 5*time.Second {
			t.Fatalf("%d requests still in flight 5s after kill", inflight.Load())
		}
		time.Sleep(time.Millisecond)
	}
	stopped := time.Since(killAt)
	sent := total.Load()
	time.Sleep(200 * time.Millisecond)
	if total.Load() != sent {
		t.Errorf("%d new requests after the kill", total.Load()-sent)
	}

	out := collect(t, r, 10*time.Second)
	ended := time.Since(killAt)
	killMu.Lock()
	var slowest time.Duration
	for _, at := range killed {
		slowest = max(slowest, at.Sub(killAt))
	}
	n := len(killed)
	killMu.Unlock()
	t.Logf("kill reached all %d workers in %v; all in-flight requests abandoned in %v; run ended %v after kill", n, slowest, stopped, ended)
	if n != 3 || slowest > time.Second {
		t.Errorf("kill reached %d workers, slowest after %v", n, slowest)
	}
	if stopped > time.Second {
		t.Errorf("load stopped %v after kill, want within 1s", stopped)
	}
	if out.res.StopReason != "killed" {
		t.Errorf("stop reason %q", out.res.StopReason)
	}
}

func TestDeadManSwitchStopsLoad(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(5 * time.Millisecond)
	}))
	defer srv.Close()

	c := newCluster(t, coordinator.Config{HeartbeatInterval: 100 * time.Millisecond})
	stoppedAt := make(chan time.Time, 1)
	var reason atomic.Value
	c.addWorker(worker.Config{Name: "solo", DeadManTimeout: time.Second, OnStop: func(_ string, _ bool, why string) {
		reason.Store(why)
		stoppedAt <- time.Now()
	}})
	c.waitConnected(1, 5*time.Second)
	r, err := c.coord.Start(context.Background(), coordinator.RunSpec{
		ID: "deadman", StartDelay: 200 * time.Millisecond, Scenario: []byte(`
metadata: {name: deadman}
target: {baseURL: "` + srv.URL + `"}
journeys: [{name: a, steps: [{get: /}]}]
load: {vus: 2, duration: 1m, gracefulStop: 1s}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(r.T0().Add(500 * time.Millisecond)))
	if hits.Load() == 0 {
		t.Fatal("no load before the server went away")
	}
	// The server vanishes; the worker cannot reconnect.
	gone := time.Now()
	c.srv.Stop()
	_ = c.lis.Close()

	select {
	case at := <-stoppedAt:
		after := at.Sub(gone)
		t.Logf("dead man's switch fired %v after the server went away (timeout 1s)", after)
		if after < 800*time.Millisecond || after > 2*time.Second {
			t.Errorf("fired after %v, want about 1s", after)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("load kept running without the server")
	}
	if why, _ := reason.Load().(string); why != worker.StopDeadMan {
		t.Errorf("stop reason %q", why)
	}
	// In-flight iterations finish within the graceful stop; then silence.
	time.Sleep(1500 * time.Millisecond)
	n := hits.Load()
	time.Sleep(500 * time.Millisecond)
	if hits.Load() != n {
		t.Errorf("load continued after the dead man's switch: %d more requests", hits.Load()-n)
	}
}
