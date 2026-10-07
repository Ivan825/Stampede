package server_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/server"
	"github.com/Ivan825/Stampede/internal/store/storetest"
	"github.com/Ivan825/Stampede/internal/worker"
)

// TestRunSplitByRegion splits a run between workers in two regions, from
// the scenario and from a run override, and refuses a region with no
// worker before recording a run.
func TestRunSplitByRegion(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
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
	for _, region := range []string{"mumbai", "frankfurt"} {
		w := worker.New(worker.Config{Server: ln.Addr().String(), Token: "join-secret", Insecure: true, Name: region, Region: region, CPUs: 1, Logger: quiet})
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
		Executor: &server.AutoExecutor{Local: &server.LocalExecutor{Logger: quiet}, Distributed: &server.DistributedExecutor{Coordinator: coord}},
		Workers:  server.CoordinatorWorkers(coord),
	})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	c := newClient(t, hs.URL)
	setup(t, c)
	var proj, tgt, sc map[string]any
	c.do("POST", "/projects", map[string]string{"name": "regions"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "t", "baseURL": target.URL}, &tgt)
	if code := c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": `
metadata: {name: regions}
journeys: [{name: a, steps: [{get: /}]}]
load:
  mode: rate
  rate: 100/s
  duration: 2s
  regions: {mumbai: 75%, frankfurt: 25%}`}, &sc); code != 201 {
		t.Fatalf("scenario %d", code)
	}

	waitRun := func(body map[string]any) map[string]any {
		t.Helper()
		var run map[string]any
		if code := c.do("POST", "/projects/"+pid+"/runs", body, &run); code != 201 {
			t.Fatalf("run %d %v", code, run)
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
		return rep
	}
	byRegion := func(rep map[string]any) map[string]float64 {
		out := map[string]float64{}
		ws, _ := rep["workers"].([]any)
		for _, w := range ws {
			m := w.(map[string]any)
			r, _ := m["region"].(string)
			out[r] += m["requests"].(float64)
		}
		return out
	}

	// The scenario's split: about 150 requests from mumbai and 50 from
	// frankfurt (shares are cut on the arrival schedule, so ±1), 200 in all.
	got := byRegion(waitRun(map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}))
	if got["mumbai"]+got["frankfurt"] != 200 || got["mumbai"] < 148 || got["mumbai"] > 152 {
		t.Errorf("scenario split: %v", got)
	}
	// A run override replaces it.
	got = byRegion(waitRun(map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"], "overrides": map[string]any{"regions": map[string]float64{"mumbai": 50, "frankfurt": 50}}}))
	if got["mumbai"] != 100 || got["frankfurt"] != 100 {
		t.Errorf("override split: %v", got)
	}

	// A region without a worker is refused, naming it; nothing is recorded.
	var e errBody
	code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"],
		"overrides": map[string]any{"regions": map[string]float64{"mumbai": 50, "virginia": 50}}}, &e)
	if code != 422 || !strings.Contains(e.Error.Message, "no connected worker is in region virginia") || !strings.Contains(e.Error.Message, "frankfurt, mumbai") {
		t.Errorf("missing region: %d %+v", code, e)
	}
	// Shares must add up to 100.
	e = errBody{}
	code = c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"],
		"overrides": map[string]any{"regions": map[string]float64{"mumbai": 50, "frankfurt": 20}}}, &e)
	if code != 422 || !strings.Contains(strings.Join(e.Error.Details, " "), "add up to 100%") {
		t.Errorf("bad shares: %d %+v", code, e)
	}
	var runs []map[string]any
	if c.do("GET", "/projects/"+pid+"/runs", nil, &runs); len(runs) != 2 {
		t.Errorf("refused runs must not be recorded: %d runs", len(runs))
	}
}

// TestRegionsNeedWorkers refuses a region split on a server that runs load
// in-process.
func TestRegionsNeedWorkers(t *testing.T) {
	base := startServer(t)
	c := newClient(t, base)
	setup(t, c)
	var proj, tgt, sc map[string]any
	c.do("POST", "/projects", map[string]string{"name": "p"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "t", "baseURL": "http://127.0.0.1:9"}, &tgt)
	c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": `
metadata: {name: split}
journeys: [{name: a, steps: [{get: /}]}]
load: {vus: 1, duration: 1s, regions: {eu: 100%}}`}, &sc)
	var e errBody
	if code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &e); code != 422 || !strings.Contains(e.Error.Message, "needs distributed workers") {
		t.Errorf("in-process region split: %d %+v", code, e)
	}
}
