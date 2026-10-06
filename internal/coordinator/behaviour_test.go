package coordinator_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	workerv1 "github.com/Ivan825/Stampede/gen/stampede/worker/v1"
	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/worker"
)

func TestSaturationSurfacedPerInterval(t *testing.T) {
	// Two users against a 200ms target cannot keep 50 arrivals a second:
	// the worker drops iterations and must say it is saturated.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	c := newCluster(t, coordinator.Config{})
	c.addWorker(worker.Config{Name: "small"})
	c.waitConnected(1, 5*time.Second)
	r, err := c.coord.Start(context.Background(), coordinator.RunSpec{
		ID: "sat", StartDelay: 300 * time.Millisecond, Scenario: []byte(`
metadata: {name: sat}
target: {baseURL: "` + srv.URL + `"}
journeys: [{name: a, steps: [{get: /}]}]
load: {mode: rate, rate: 50/s, duration: 3s, maxVUs: 2, gracefulStop: 2s}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawInWorkers atomic.Bool
	go func() {
		// The heartbeat carries the same verdict to Workers().
		for i := 0; i < 40 && !sawInWorkers.Load(); i++ {
			for _, w := range c.coord.Workers() {
				if w.Health.Saturated {
					sawInWorkers.Store(true)
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	out := collect(t, r, 20*time.Second)
	if out.err != nil {
		t.Fatal(out.err)
	}
	sat := out.eventsOf(coordinator.EventWorkerSaturated)
	if len(sat) < 3 {
		t.Fatalf("%d saturation events, want one per interval of the 3s run", len(sat))
	}
	seen := map[int64]bool{}
	for _, e := range sat {
		if seen[e.Interval] {
			t.Errorf("two saturation events for interval %d", e.Interval)
		}
		seen[e.Interval] = true
		if !strings.Contains(strings.Join(e.Reasons, ";"), "dropped") {
			t.Errorf("interval %d reasons %v", e.Interval, e.Reasons)
		}
	}
	ws := out.res.Workers[0]
	if len(ws.Saturated) == 0 || ws.Saturated[0].From != 0 || ws.Saturated[0].To < 2 {
		t.Errorf("saturated windows %+v", ws.Saturated)
	}
	if len(ws.SaturationReasons) == 0 || ws.SaturationReasons[0] != "iterations dropped" {
		t.Errorf("reasons %v", ws.SaturationReasons)
	}
	if !sawInWorkers.Load() {
		t.Error("Workers() never showed the worker as saturated")
	}
}

func TestKillBeforeT0StartsNothing(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	c := newCluster(t, coordinator.Config{})
	c.addWorker(worker.Config{Name: "a"})
	c.addWorker(worker.Config{Name: "b"})
	c.waitConnected(2, 5*time.Second)
	r, err := c.coord.Start(context.Background(), coordinator.RunSpec{
		ID: "early", Scenario: rateScenario(srv.URL, 100, 5), StartDelay: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	killAt := time.Now()
	r.Kill()
	out := collect(t, r, 10*time.Second)
	if took := time.Since(killAt); took > time.Second || !time.Now().Before(r.T0()) {
		t.Errorf("run ended %v after a kill before T0", took)
	}
	if out.res.StopReason != "killed" || hits.Load() != 0 {
		t.Errorf("reason %q, %d requests", out.res.StopReason, hits.Load())
	}
	time.Sleep(time.Until(r.T0().Add(300 * time.Millisecond)))
	if hits.Load() != 0 {
		t.Errorf("%d requests after T0 of a killed run", hits.Load())
	}
}

func TestSilentWorkerIsLostAfterThreeHeartbeats(t *testing.T) {
	hb := 100 * time.Millisecond
	c := newCluster(t, coordinator.Config{HeartbeatInterval: hb})
	silent := c.fake("silent", "", "", "")
	steady := c.fake("steady", "", "", "")
	defer silent.disconnect()
	defer steady.disconnect()
	c.waitConnected(2, 5*time.Second)

	// The steady worker heartbeats throughout; the silent one never does,
	// though its connection stays open.
	stopBeat := make(chan struct{})
	defer close(stopBeat)
	go func() {
		tick := time.NewTicker(hb)
		defer tick.Stop()
		for {
			select {
			case <-stopBeat:
				return
			case <-tick.C:
				steady.send(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_Heartbeat{Heartbeat: &workerv1.Heartbeat{}}})
			}
		}
	}()

	ch := make(chan *coordinator.Run, 1)
	go func() {
		r, err := c.coord.Start(context.Background(), coordinator.RunSpec{ID: "silent", Scenario: []byte(tinyScenario), StartDelay: 200 * time.Millisecond})
		if err != nil {
			t.Error(err)
		}
		ch <- r
	}()
	<-silent.starts
	<-steady.starts
	r := <-ch
	if r == nil {
		t.FailNow()
	}
	silent.snapshot("silent", 0, 1, 3)
	steady.snapshot("silent", 0, 1, 4)
	lastHeard := time.Now()

	var lostAt time.Time
	events := r.Events()
	for e := range events {
		if e.Type == coordinator.EventWorkerLost {
			lostAt = e.Time
			if e.WorkerName != "silent" {
				t.Errorf("lost %s", e.WorkerName)
			}
			break
		}
	}
	silentFor := lostAt.Sub(lastHeard)
	t.Logf("silent worker declared lost after %v (heartbeat %v)", silentFor, hb)
	if silentFor < 3*hb || silentFor > 6*hb {
		t.Errorf("declared lost after %v, want just over 3 heartbeats (%v)", silentFor, 3*hb)
	}
	// The lost worker is told to stop, in case it is alive but cut off.
	select {
	case <-silent.stops:
	case <-time.After(2 * time.Second):
		t.Error("lost worker was not told to stop")
	}
	steady.snapshot("silent", 1, 2, 4)
	steady.send(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_RunFinished{RunFinished: &workerv1.RunFinished{RunId: "silent", StopReason: "completed", LastSeq: 2}}})
	for range events {
	}
	res, err := r.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var reqs uint64
	for _, s := range res.Snapshots {
		reqs += s.Totals().Requests
	}
	// The silent worker's data before the loss is kept.
	if reqs != 11 || !res.Degraded {
		t.Errorf("requests %d degraded %v", reqs, res.Degraded)
	}
	for _, ws := range res.Workers {
		if ws.Name == "silent" && (ws.State != coordinator.WorkerLost || ws.Lost == nil || ws.Lost.From != 0) {
			t.Errorf("silent worker summary %+v", ws)
		}
	}
}

func TestWorkerReconnectsAfterServerRestart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
	}))
	defer srv.Close()
	c := newCluster(t, coordinator.Config{})
	stopped := make(chan string, 4)
	c.addWorker(worker.Config{Name: "phoenix", OnStop: func(_ string, kill bool, why string) {
		if kill {
			stopped <- why
		}
	}})
	c.waitConnected(1, 5*time.Second)
	r, err := c.coord.Start(context.Background(), coordinator.RunSpec{
		ID: "orphan", StartDelay: 200 * time.Millisecond, Scenario: []byte(`
metadata: {name: orphan}
target: {baseURL: "` + srv.URL + `"}
journeys: [{name: a, steps: [{get: /}]}]
load: {vus: 1, duration: 1m}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(r.T0().Add(300 * time.Millisecond)))

	// The server restarts and has never heard of the run, so the
	// returning worker must not carry on with it.
	restarted := time.Now()
	c.restart(coordinator.Config{})
	c.waitConnected(1, 10*time.Second)
	t.Logf("worker reconnected %v after the server restart", time.Since(restarted).Round(time.Millisecond))
	select {
	case why := <-stopped:
		t.Logf("orphaned run killed %v after the restart: %s", time.Since(restarted).Round(time.Millisecond), why)
		if !strings.Contains(why, "no longer tracks") {
			t.Errorf("stop reason %q", why)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the new server did not stop the orphaned run")
	}
	if ws := c.coord.Workers(); len(ws) != 1 || ws[0].Name != "phoenix" || !ws[0].Connected {
		t.Errorf("workers after restart %+v", ws)
	}
}
