package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestDryRunGate: with the project setting on, a run first dry-runs each
// journey once with one user; a failing journey fails the run before any
// load, and a passing dry run lets load start. The result is recorded as
// run events.
func TestDryRunGate(t *testing.T) {
	var ok, broken atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			select {
			case <-r.Context().Done():
			case <-time.After(15 * time.Second):
			}
			return
		}
		if r.URL.Path == "/broken" {
			broken.Add(1)
			w.WriteHeader(500)
			return
		}
		ok.Add(1)
	}))
	defer target.Close()
	base := startServer(t)
	c := newClient(t, base)
	setup(t, c)
	var proj, tgt, good, bad map[string]any
	c.do("POST", "/projects", map[string]string{"name": "gated"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "t", "baseURL": target.URL}, &tgt)
	c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": `
metadata: {name: good}
journeys: [{name: home, steps: [{get: /, check: {status: 200}}]}]
load: {mode: rate, rate: 20/s, duration: 1s}`}, &good)
	c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": `
metadata: {name: bad}
journeys:
  - {name: home, steps: [{get: /, check: {status: 200}}]}
  - {name: orders, steps: [{get: /broken, check: {status: 200}}]}
load: {mode: rate, rate: 50/s, duration: 2s}`}, &bad)
	if code := c.do("PUT", "/projects/"+pid+"/settings", map[string]any{"caps": map[string]any{}, "requireDryRun": true}, nil); code != 200 {
		t.Fatalf("settings: %d", code)
	}

	finish := func(sc map[string]any) map[string]any {
		t.Helper()
		var run map[string]any
		if code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &run); code != 201 {
			t.Fatalf("run: %d %v", code, run)
		}
		var got map[string]any
		for start := time.Now(); ; time.Sleep(50 * time.Millisecond) {
			c.do("GET", "/runs/"+run["id"].(string), nil, &got)
			if s := got["status"]; s == "completed" || s == "failed" || s == "aborted" {
				return got
			}
			if time.Since(start) > 30*time.Second {
				t.Fatalf("run stuck in %v", got["status"])
			}
		}
	}

	// A failing journey stops the run before load: the target sees the
	// dry run's requests only.
	got := finish(bad)
	msg, _ := got["error"].(string)
	if got["status"] != "failed" || !strings.Contains(msg, "the required dry run failed, so no load was started: orders: step GET /broken") {
		t.Fatalf("gated run: %v %q", got["status"], msg)
	}
	if broken.Load() != 1 || ok.Load() != 1 {
		t.Errorf("only the dry run should reach the target: /broken %d, / %d", broken.Load(), ok.Load())
	}
	var events []map[string]any
	c.do("GET", "/runs/"+got["id"].(string)+"/events", nil, &events)
	var types []string
	for _, e := range events {
		types = append(types, e["type"].(string))
	}
	if strings.Join(types, ",") != "dryrun.started,dryrun.journey,dryrun.journey,dryrun.failed" {
		t.Errorf("events: %v", types)
	}
	if d, _ := events[2]["details"].(map[string]any); d["journey"] != "orders" || d["ok"] != false {
		t.Errorf("failed journey event: %v", events[2])
	}

	// A passing dry run lets load start.
	ok.Store(0)
	got = finish(good)
	if got["status"] != "completed" || ok.Load() < 20 {
		t.Errorf("passing run: %v %v, %d requests", got["status"], got["error"], ok.Load())
	}
	c.do("GET", "/runs/"+got["id"].(string)+"/events", nil, &events)
	if len(events) < 3 || events[2]["type"] != "dryrun.passed" {
		t.Errorf("passing events: %v", events)
	}

	// Killing a run during its dry run aborts it before any load.
	var slow map[string]any
	c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": `
metadata: {name: slow}
journeys: [{name: wait, steps: [{get: /slow, timeout: 20s}]}]
load: {mode: rate, rate: 20/s, duration: 1s}`}, &slow)
	var run map[string]any
	c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": slow["id"], "targetId": tgt["id"]}, &run)
	rid := run["id"].(string)
	eventually(t, 10*time.Second, "the dry run to start", func() bool {
		var evs []map[string]any
		c.do("GET", "/runs/"+rid+"/events", nil, &evs)
		return len(evs) > 0
	})
	if code := c.do("POST", "/runs/"+rid+"/kill", nil, nil); code != 202 {
		t.Fatalf("kill: %d", code)
	}
	eventually(t, 10*time.Second, "the run to abort", func() bool {
		c.do("GET", "/runs/"+rid, nil, &run)
		return run["status"] == "aborted"
	})
	if e, _ := run["error"].(string); !strings.Contains(e, "killed during its dry run, before any load") {
		t.Errorf("killed run: %v", run)
	}

	// With the setting off, runs start straight away.
	c.do("PUT", "/projects/"+pid+"/settings", map[string]any{"caps": map[string]any{}, "requireDryRun": false}, nil)
	broken.Store(0)
	got = finish(bad)
	if got["status"] != "completed" || broken.Load() < 10 {
		t.Errorf("ungated run: %v, %d requests to /broken", got["status"], broken.Load())
	}
}
