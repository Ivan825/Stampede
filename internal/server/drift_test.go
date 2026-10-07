package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/ai/provider"
	"github.com/Ivan825/Stampede/internal/server"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

const (
	driftSpecV1 = `{"openapi":"3.0.3","info":{"title":"shop","version":"1"},"paths":{
  "/items":{"get":{"responses":{"200":{"description":"ok"}}}},
  "/orders":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	driftSpecV2 = `{"openapi":"3.0.3","info":{"title":"shop","version":"2"},"paths":{
  "/items":{"get":{"responses":{"200":{"description":"ok"}}}},
  "/v2/orders":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	driftScenario = `
metadata: {name: shop}
journeys:
  - {name: items, steps: [{get: /items, check: {status: 200}}]}
  - {name: orders, steps: [{get: /orders, check: {status: 200}}]}
load: {vus: 1, duration: 10s}`
	// The repaired scenario the fake model proposes.
	driftRepaired = `{"metadata":{"name":"shop"},"journeys":[
  {"name":"items","steps":[{"get":"/items","check":{"status":200}}]},
  {"name":"orders","steps":[{"get":"/v2/orders","check":{"status":200}}]}],
  "load":{"vus":1,"duration":"10s"}}`
)

// TestScheduledDrift: a drift schedule dry-runs the scenario against the
// target and diffs the API spec on its cron, records results, notifies
// drift.detected subscribers when journeys broke, and offers an AI repair
// that is proposed, not saved.
func TestScheduledDrift(t *testing.T) {
	var v2 atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		if v2.Load() {
			_, _ = w.Write([]byte(driftSpecV2))
			return
		}
		_, _ = w.Write([]byte(driftSpecV1))
	})
	mux.HandleFunc("GET /items", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET /orders", func(w http.ResponseWriter, _ *http.Request) {
		if v2.Load() {
			w.WriteHeader(404)
		}
	})
	mux.HandleFunc("GET /v2/orders", func(http.ResponseWriter, *http.Request) {})
	shop := httptest.NewServer(mux)
	defer shop.Close()

	var hookMu sync.Mutex
	var hooks []map[string]any
	hookSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		var ev map[string]any
		_ = json.NewDecoder(r.Body).Decode(&ev)
		hookMu.Lock()
		hooks = append(hooks, ev)
		hookMu.Unlock()
	}))
	defer hookSrv.Close()

	fakes := &aiFakes{}
	clock := newFakeClock()
	st := storetest.Open(t)
	key, _ := keyringKey()
	srv, err := server.New(server.Config{
		Store: st, Keyring: key, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: clock.Now, SchedulerInterval: 20 * time.Millisecond,
		AI: server.AIConfig{NewProvider: fakes.build, Workers: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv.StartScheduler(context.Background())
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		hs.Close()
	})

	c := newClient(t, hs.URL)
	setup(t, c)
	var proj, tgt, sc map[string]any
	c.do("POST", "/projects", map[string]string{"name": "shop"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "t", "baseURL": shop.URL}, &tgt)
	if code := c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": driftScenario}, &sc); code != 201 {
		t.Fatalf("scenario: %d", code)
	}
	if code := c.do("POST", "/notifications/channels", map[string]any{"name": "ops", "kind": "webhook", "url": hookSrv.URL, "allowPrivate": true, "events": []string{"drift.detected"}}, nil); code != 201 {
		t.Fatalf("channel: %d", code)
	}

	body := map[string]any{"name": "nightly-drift", "kind": "drift", "scenarioId": sc["id"], "targetId": tgt["id"], "cron": "* * * * *", "specURL": shop.URL + "/openapi.json"}
	var e errBody
	bad := map[string]any{}
	for k, v := range body {
		bad[k] = v
	}
	bad["specURL"] = "https://example.org/openapi.json"
	if code := c.do("POST", "/projects/"+pid+"/schedules", bad, &e); code != 422 || !strings.Contains(e.Error.Message, "target's host") {
		t.Errorf("spec on another host: %d %+v", code, e)
	}
	bad["kind"], bad["specURL"] = "run", shop.URL+"/openapi.json"
	if code := c.do("POST", "/projects/"+pid+"/schedules", bad, &e); code != 422 {
		t.Errorf("specURL on a run schedule: %d", code)
	}
	var sch map[string]any
	if code := c.do("POST", "/projects/"+pid+"/schedules", body, &sch); code != 201 || sch["kind"] != "drift" {
		t.Fatalf("drift schedule: %d %v", code, sch)
	}
	sid := sch["id"].(string)

	// First check by hand: everything works, and the spec is remembered.
	var res map[string]any
	if code := c.do("POST", "/schedules/"+sid+"/run", nil, &res); code != 200 || res["status"] != "ok" {
		t.Fatalf("first check: %d %v", code, res)
	}

	// The API changes. The scheduler's next firing finds the break.
	v2.Store(true)
	clock.Advance(time.Minute)
	eventually(t, 10*time.Second, "the scheduled drift check", func() bool {
		c.do("GET", "/schedules/"+sid, nil, &sch)
		return sch["lastDriftStatus"] == "drifted"
	})
	if b, _ := sch["lastDriftBroken"].([]any); len(b) != 1 || b[0] != "orders" {
		t.Errorf("broken: %v", sch["lastDriftBroken"])
	}
	var runs []map[string]any
	if c.do("GET", "/projects/"+pid+"/runs", nil, &runs); len(runs) != 0 {
		t.Errorf("a drift schedule must not start load: %d runs", len(runs))
	}
	var results []map[string]any
	c.do("GET", "/projects/"+pid+"/drift-results?scheduleId="+sid, nil, &results)
	if len(results) != 2 || results[0]["status"] != "drifted" || results[1]["status"] != "ok" {
		t.Fatalf("results: %v", results)
	}
	did := results[0]["id"].(string)
	var full map[string]any
	c.do("GET", "/drift-results/"+did, nil, &full)
	if rm, _ := full["removedEndpoints"].([]any); len(rm) != 1 || rm[0] != "GET /orders" {
		t.Errorf("removed endpoints: %v", full["removedEndpoints"])
	}
	if um, _ := full["unmatched"].([]any); len(um) != 1 || um[0] != "orders: GET /orders" {
		t.Errorf("unmatched: %v", full["unmatched"])
	}
	js, _ := full["journeys"].([]any)
	var orders map[string]any
	for _, j := range js {
		if m := j.(map[string]any); m["journey"] == "orders" {
			orders = m
		}
	}
	if orders == nil || orders["ok"] != false || !strings.Contains(orders["problem"].(string), "404") || orders["traces"] == nil {
		t.Errorf("orders journey: %v", orders)
	}

	eventually(t, 10*time.Second, "the drift notification", func() bool {
		hookMu.Lock()
		defer hookMu.Unlock()
		return len(hooks) > 0
	})
	hookMu.Lock()
	ev := hooks[0]
	hookMu.Unlock()
	d, _ := ev["drift"].(map[string]any)
	if ev["type"] != "drift.detected" || d["schedule"] != "nightly-drift" || len(d["broken"].([]any)) != 1 {
		t.Errorf("notification: %v", ev)
	}

	// Repair: nothing to repair on an ok result; the drifted one starts
	// an AI job whose proposal is not saved until approved.
	if code := c.do("POST", "/drift-results/"+results[1]["id"].(string)+"/repair", nil, &e); code != 409 {
		t.Errorf("repairing an ok result: %d", code)
	}
	fake := &provider.Fake{Replies: []any{driftRepaired}}
	fakes.set(fake)
	if code := c.do("POST", "/ai/providers", map[string]any{"kind": "anthropic", "apiKey": "sk-test", "model": "m"}, nil); code != 201 {
		t.Fatalf("provider: %d", code)
	}
	viewer := member(t, c, hs.URL, "viewer@acme.test", "viewer")
	if code := viewer.do("POST", "/drift-results/"+did+"/repair", nil, nil); code != 403 {
		t.Errorf("viewer repairs: %d", code)
	}
	var job map[string]any
	if code := c.do("POST", "/drift-results/"+did+"/repair", map[string]any{}, &job); code != 202 || job["scenarioId"] != sc["id"] {
		t.Fatalf("repair: %d %v", code, job)
	}
	done := waitJob(t, c, job["id"].(string))
	if done.Status != "succeeded" || !strings.Contains(done.Diff, "+") || !strings.Contains(done.Diff, "/v2/orders") {
		t.Errorf("repair job: %s %q diff:\n%s", done.Status, done.Error, done.Diff)
	}
	reqs := fake.Requests()
	if len(reqs) == 0 || !strings.Contains(reqs[0].Messages[0].Content, "Repair only the broken journeys (orders)") || !strings.Contains(reqs[0].Messages[0].Content, "/v2/orders") {
		t.Errorf("the model should get the brief and the current spec: %v", reqs)
	}
	c.do("GET", "/drift-results/"+did, nil, &full)
	if full["repairJobId"] != job["id"] {
		t.Errorf("repair job not recorded: %v", full["repairJobId"])
	}
	var versions []map[string]any
	if c.do("GET", "/scenarios/"+sc["id"].(string)+"/versions", nil, &versions); len(versions) != 1 {
		t.Errorf("the repair must not save a version by itself: %d versions", len(versions))
	}
}
