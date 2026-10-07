package coordinator_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/worker"
)

// A worker lost mid-run hands its share to an idle one, which picks up
// from a later interval: arrivals before that are the lost worker's (sent
// or missed), arrivals after it are the spare's, and none is sent twice.
func TestSpareTakesOverLostShare(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()

	hb := 200 * time.Millisecond
	c := newCluster(t, coordinator.Config{HeartbeatInterval: hb, TakeoverLead: 500 * time.Millisecond})
	workers := map[string]*testWorker{}
	for _, name := range []string{"a", "b", "spare"} {
		workers[name] = c.addWorker(worker.Config{Name: name, CPUs: 1})
	}
	c.waitConnected(3, 5*time.Second)

	const rate, secs = 200, 6
	r, err := c.coord.Start(context.Background(), coordinator.RunSpec{
		ID: "takeover", Scenario: rateScenario(srv.URL, rate, secs), StartDelay: 300 * time.Millisecond, Workers: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The coordinator picks two of the three; kill one of them at 1.5s.
	var victim string
	for _, w := range c.coord.Workers() {
		if w.CurrentRun == "takeover" && victim == "" {
			victim = w.Name
		}
	}
	time.AfterFunc(time.Until(r.T0().Add(1500*time.Millisecond)), workers[victim].cancel)
	out := collect(t, r, 30*time.Second)
	if out.err != nil {
		t.Fatal(out.err)
	}
	res := out.res

	var lost, spare *coordinator.WorkerSummary
	var sum uint64
	for i := range res.Workers {
		ws := &res.Workers[i]
		sum += ws.Iterations
		switch {
		case ws.Name == victim:
			lost = ws
		case ws.Replaces != "":
			spare = ws
		default:
			if exp := expectedArrivals(rate*secs, ws.ShareLo, ws.ShareHi); ws.State != coordinator.WorkerFinished || ws.Iterations != exp {
				t.Errorf("%s: state %s, %d iterations, want exactly %d", ws.Name, ws.State, ws.Iterations, exp)
			}
		}
	}
	if lost == nil || spare == nil {
		t.Fatalf("workers %+v", res.Workers)
	}
	if lost.State != coordinator.WorkerLost || spare.State != coordinator.WorkerFinished || spare.Replaces != victim {
		t.Fatalf("lost %+v spare %+v", *lost, *spare)
	}
	if spare.ShareLo != lost.ShareLo || spare.ShareHi != lost.ShareHi {
		t.Errorf("spare share [%v, %v), lost [%v, %v)", spare.ShareLo, spare.ShareHi, lost.ShareLo, lost.ShareHi)
	}
	// The spare sends exactly the share's arrivals from its start on.
	full := expectedArrivals(rate*secs, spare.ShareLo, spare.ShareHi)
	before := expectedArrivals(int(spare.JoinedAt)*rate, spare.ShareLo, spare.ShareHi)
	if spare.Iterations != full-before {
		t.Errorf("spare joined at interval %d and ran %d iterations, want exactly %d", spare.JoinedAt, spare.Iterations, full-before)
	}
	// The gap is the window between the loss and the takeover.
	if lost.Lost == nil || lost.Lost.From != 1 || lost.Lost.To != spare.JoinedAt-1 {
		t.Errorf("lost window %+v, spare joined at %d", lost.Lost, spare.JoinedAt)
	}
	if got := started(res.Snapshots); got != sum {
		t.Errorf("merged %d iterations, workers account for %d", got, sum)
	}
	// The server also saw what the lost worker sent after its last report
	// arrived, never more than the plan.
	if h := uint64(hits.Load()); h < sum || h > rate*secs {
		t.Errorf("server saw %d requests, workers account for %d of %d planned", h, sum, rate*secs)
	}
	if ev := out.eventsOf(coordinator.EventTakeover); len(ev) != 1 || !strings.Contains(ev[0].Message, "takes over") {
		t.Errorf("takeover events %+v", ev)
	}
	if deg := out.eventsOf(coordinator.EventDegraded); len(deg) != 0 {
		t.Errorf("degraded events %+v", deg)
	}
	t.Logf("lost at interval %d, spare from %d: %d of %d planned iterations sent",
		lost.Lost.From, spare.JoinedAt, sum, rate*secs)
}

// Without a spare the run continues degraded and says why.
func TestNoSpareMeansDegraded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	c := newCluster(t, coordinator.Config{HeartbeatInterval: 200 * time.Millisecond, TakeoverLead: 300 * time.Millisecond})
	a := c.addWorker(worker.Config{Name: "a", CPUs: 1})
	c.addWorker(worker.Config{Name: "b", CPUs: 1})
	c.waitConnected(2, 5*time.Second)
	r, err := c.coord.Start(context.Background(), coordinator.RunSpec{
		ID: "nospare", Scenario: rateScenario(srv.URL, 50, 6), StartDelay: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(time.Until(r.T0().Add(time.Second)), a.cancel)
	out := collect(t, r, 30*time.Second)
	deg := out.eventsOf(coordinator.EventDegraded)
	if len(deg) != 1 || !strings.Contains(deg[0].Message, "no spare worker is connected") {
		t.Errorf("degraded events %+v", deg)
	}
}
