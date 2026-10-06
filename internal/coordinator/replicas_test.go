package coordinator_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/worker"
)

// TestWorkerSharedByTwoReplicas: one worker process connected to two
// server replicas runs load for one at a time, and the other does not pick
// it while it is busy.
func TestWorkerSharedByTwoReplicas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	a := newCluster(t, coordinator.Config{HeartbeatInterval: 100 * time.Millisecond})
	b := newCluster(t, coordinator.Config{HeartbeatInterval: 100 * time.Millisecond})
	gate := &worker.Gate{}
	a.addWorker(worker.Config{Name: "shared", Gate: gate})
	b.addWorker(worker.Config{Name: "shared", Gate: gate})
	a.waitConnected(1, 5*time.Second)
	b.waitConnected(1, 5*time.Second)

	ra, err := a.coord.Start(context.Background(), coordinator.RunSpec{ID: "on-a", StartDelay: 200 * time.Millisecond, Scenario: rateScenario(srv.URL, 20, 2)})
	if err != nil {
		t.Fatal(err)
	}
	// Once the worker's heartbeat to B says it is busy, B finds no worker.
	time.Sleep(500 * time.Millisecond)
	if _, err := b.coord.Start(context.Background(), coordinator.RunSpec{ID: "on-b", Scenario: rateScenario(srv.URL, 20, 1)}); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("B started a run on a busy worker: %v", err)
	}
	if out := collect(t, ra, 20*time.Second); out.err != nil {
		t.Fatal(out.err)
	}
	// Free again: B can use it.
	deadline := time.Now().Add(5 * time.Second)
	for {
		rb, err := b.coord.Start(context.Background(), coordinator.RunSpec{ID: "on-b", StartDelay: 200 * time.Millisecond, Scenario: rateScenario(srv.URL, 20, 1)})
		if err == nil {
			if out := collect(t, rb, 20*time.Second); out.err != nil {
				t.Fatal(out.err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("B never got the worker back: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
