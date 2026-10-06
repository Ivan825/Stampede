package server_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/server"
	"github.com/Ivan825/Stampede/internal/store/storetest"
	"github.com/Ivan825/Stampede/internal/worker"
)

// TestRunOnWorkers drives a run through the API onto two real workers
// connected over gRPC and checks the merged result is exact.
func TestRunOnWorkers(t *testing.T) {
	var hits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	coord := coordinator.New(coordinator.Config{JoinToken: "join-secret", Logger: quiet})
	gs := grpc.NewServer(coordinator.ServerOptions(nil)...)
	coord.Register(gs)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go gs.Serve(ln) //nolint:errcheck
	defer gs.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < 2; i++ {
		w := worker.New(worker.Config{Server: ln.Addr().String(), Token: "join-secret", Insecure: true, Name: fmt.Sprintf("w%d", i), CPUs: 1, Logger: quiet})
		go w.Run(ctx) //nolint:errcheck
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(coord.Workers()) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("workers did not register")
		}
		time.Sleep(20 * time.Millisecond)
	}

	st := storetest.Open(t)
	key, _ := keyringKey()
	srv, err := server.New(server.Config{
		Store: st, Keyring: key, Logger: quiet,
		Executor: &server.DistributedExecutor{Coordinator: coord},
		Workers:  server.CoordinatorWorkers(coord),
	})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	c := newClient(t, hs.URL)
	setup(t, c)
	var workers []map[string]any
	if c.do("GET", "/workers", nil, &workers); len(workers) != 2 || workers[0]["status"] != "idle" {
		t.Fatalf("workers: %v", workers)
	}
	var proj, tgt, sc, run map[string]any
	c.do("POST", "/projects", map[string]string{"name": "dist"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "t", "baseURL": target.URL}, &tgt)
	if code := c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": `
metadata: {name: dist}
journeys: [{name: a, steps: [{get: /}]}]
load: {mode: rate, rate: 100/s, duration: 3s}`}, &sc); code != 201 {
		t.Fatalf("scenario %d", code)
	}
	if code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &run); code != 201 {
		t.Fatalf("run %d", code)
	}
	rid := run["id"].(string)
	var got map[string]any
	for start := time.Now(); ; time.Sleep(100 * time.Millisecond) {
		c.do("GET", "/runs/"+rid, nil, &got)
		if s := got["status"]; s == "completed" || s == "failed" || s == "aborted" {
			break
		}
		if time.Since(start) > 30*time.Second {
			t.Fatalf("run stuck in %v", got["status"])
		}
	}
	if got["status"] != "completed" {
		t.Fatalf("run %v: %v", got["status"], got["error"])
	}
	var rep map[string]any
	c.do("GET", "/runs/"+rid+"/report", nil, &rep)
	overall := rep["overall"].(map[string]any)
	if overall["requests"].(float64) != 300 || hits.Load() != 300 {
		t.Errorf("want exactly 300 requests, report %v, target saw %d", overall["requests"], hits.Load())
	}
	if ws, _ := rep["workers"].([]any); len(ws) != 2 {
		t.Errorf("report should list 2 workers: %v", rep["workers"])
	}
}

func TestFileFeedersConfined(t *testing.T) {
	base := startServer(t)
	c := newClient(t, base)
	setup(t, c)
	var proj, tgt, sc map[string]any
	c.do("POST", "/projects", map[string]string{"name": "p"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "t", "baseURL": "http://127.0.0.1:9"}, &tgt)
	c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": `
metadata: {name: steal}
data: {pw: {csv: /etc/passwd}}
journeys: [{name: a, steps: [{get: "/?x=${data.pw}"}]}]
load: {iterations: 1}`}, &sc)
	var e errBody
	if code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &e); code != 422 {
		t.Errorf("file feeder without a data dir must be refused: %d %+v", code, e)
	}
}
