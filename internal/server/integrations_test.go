package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/notify"
	"github.com/Ivan825/Stampede/internal/server"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

func startNotifyServer(t *testing.T) string {
	t.Helper()
	st := storetest.Open(t)
	key, _ := keyringKey()
	srv, err := server.New(server.Config{
		Store: st, Keyring: key, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notify: server.NotifyConfig{
			Sender:    &notify.Sender{Backoff: []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}},
			PublicURL: "https://stampede.example.com/",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		hs.Close()
	})
	return hs.URL
}

// hookReceiver records webhook deliveries; the first failFirst requests
// get a 503.
type hookReceiver struct {
	mu        sync.Mutex
	events    []notify.Event
	sigOK     []bool
	failFirst int
	n         int
	secret    string
}

func (h *hookReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.n++
	if h.n <= h.failFirst {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	var ev notify.Event
	_ = json.Unmarshal(b, &ev)
	h.events = append(h.events, ev)
	h.sigOK = append(h.sigOK, notify.Verify([]byte(h.secret), b, r.Header.Get(notify.HeaderSignature)))
}

func (h *hookReceiver) types() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, e := range h.events {
		out = append(out, e.Type)
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestIntegrationsAndNotifications(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") }))
	defer target.Close()
	var promAuth string
	var promMu sync.Mutex
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		promMu.Lock()
		promAuth = r.Header.Get("Authorization")
		promMu.Unlock()
		start, _ := strconv.ParseFloat(r.Form.Get("start"), 64)
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[%f,"1"],[%f,"2"]]}]}}`, start, start+1)
	}))
	defer prom.Close()
	hook := &hookReceiver{failFirst: 1}
	hooks := httptest.NewServer(hook)
	defer hooks.Close()

	base := startNotifyServer(t)
	c := newClient(t, base)
	setup(t, c)

	// Integrations: admin only, tokens never returned, names unique.
	var integ map[string]any
	if code := c.do("POST", "/integrations", map[string]any{"name": "prom", "kind": "prometheus", "url": prom.URL + "/", "bearerToken": "prom-token"}, &integ); code != 201 {
		t.Fatalf("create prometheus integration: %d %v", code, integ)
	}
	if integ["hasToken"] != true || integ["url"] != prom.URL || integ["bearerToken"] != nil {
		t.Errorf("integration %v", integ)
	}
	if code := c.do("POST", "/integrations", map[string]any{"name": "jaeger", "kind": "traces", "url": "https://jaeger.example.com/trace/{traceId}"}, nil); code != 201 {
		t.Fatalf("create traces integration: %d", code)
	}
	var e errBody
	for _, bad := range []map[string]any{
		{"name": "prom", "kind": "prometheus", "url": prom.URL},
		{"name": "t2", "kind": "traces", "url": "https://jaeger.example.com/trace/"},
		{"name": "p2", "kind": "prometheus", "url": "ftp://prom"},
		{"name": "p3", "kind": "prometheus", "url": "http://u:p@prom:9090"},
		{"name": "bad name!", "kind": "prometheus", "url": "http://prom:9090"},
	} {
		if code := c.do("POST", "/integrations", bad, &e); code != 409 && code != 422 {
			t.Errorf("bad integration %v accepted: %d", bad, code)
		}
	}
	var list []map[string]any
	if c.do("GET", "/integrations", nil, &list); len(list) != 2 {
		t.Errorf("integrations %v", list)
	}

	// Notification channels: a loopback destination needs allowPrivate.
	if code := c.do("POST", "/notifications/channels", map[string]any{"name": "ops", "kind": "webhook", "url": hooks.URL}, &e); code != 422 || !strings.Contains(e.Error.Message, "allowPrivate") {
		t.Errorf("loopback webhook without allowPrivate: %d %+v", code, e)
	}
	var created struct {
		Channel map[string]any `json:"channel"`
		Secret  string         `json:"secret"`
	}
	if code := c.do("POST", "/notifications/channels", map[string]any{"name": "ops", "kind": "webhook", "url": hooks.URL + "/hook?token=abc", "allowPrivate": true}, &created); code != 201 {
		t.Fatalf("create channel: %d", code)
	}
	if !strings.HasPrefix(created.Secret, "whsec_") || created.Channel["urlHint"] != hooks.URL || created.Channel["hasSecret"] != true || len(created.Channel["events"].([]any)) != 3 {
		t.Errorf("channel %+v", created)
	}
	hook.mu.Lock()
	hook.secret = created.Secret
	hook.mu.Unlock()
	chID := created.Channel["id"].(string)

	// Send test: one attempt, recorded, no retry (the receiver fails the first).
	var d map[string]any
	if code := c.do("POST", "/notifications/channels/"+chID+"/test", nil, &d); code != 200 || d["ok"] != false || d["statusCode"].(float64) != 503 {
		t.Fatalf("test send: %d %v", code, d)
	}
	if code := c.do("POST", "/notifications/channels/"+chID+"/test", nil, &d); code != 200 || d["ok"] != true || d["event"] != "test" {
		t.Fatalf("second test send: %d %v", code, d)
	}

	// A run that misses a target: run.finished and run.target_failed, with
	// target metrics and trace links in the report.
	var proj, tgt, sc map[string]any
	c.do("POST", "/projects", map[string]string{"name": "Shop"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "local", "baseURL": target.URL}, &tgt)
	yaml := `
metadata: {name: observed}
journeys: [{name: j, steps: [{get: /}]}]
load: {mode: rate, rate: 20/s, duration: 2s}
targets: ["p95 < 1us"]
observe:
  prometheus: {integration: prom, queries: {cpu: 'rate(process_cpu_seconds_total[30s])'}}
  traces: {integration: jaeger}`
	if code := c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": yaml}, &sc); code != 201 {
		t.Fatalf("scenario: %d", code)
	}
	var run map[string]any
	if code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &run); code != 201 {
		t.Fatalf("run: %d %v", code, run)
	}
	rid := run["id"].(string)
	waitFor(t, "run.finished and run.target_failed", func() bool {
		ts := hook.types()
		return strings.Contains(strings.Join(ts, ","), "run.finished") && strings.Contains(strings.Join(ts, ","), "run.target_failed")
	})
	var rep struct {
		TargetMetrics []struct {
			Name   string
			Points []map[string]float64
			Error  string
		} `json:"targetMetrics"`
		Journeys []struct {
			Steps []struct {
				Slowest []struct {
					TraceID  string `json:"traceId"`
					TraceURL string `json:"traceUrl"`
				}
			}
		}
	}
	if code := c.do("GET", "/runs/"+rid+"/report", nil, &rep); code != 200 {
		t.Fatalf("report: %d", code)
	}
	if len(rep.TargetMetrics) != 1 || rep.TargetMetrics[0].Name != "cpu" || len(rep.TargetMetrics[0].Points) != 2 || rep.TargetMetrics[0].Error != "" {
		t.Errorf("target metrics %+v", rep.TargetMetrics)
	}
	promMu.Lock()
	if promAuth != "Bearer prom-token" {
		t.Errorf("prometheus got Authorization %q", promAuth)
	}
	promMu.Unlock()
	sl := rep.Journeys[0].Steps[0].Slowest
	if len(sl) == 0 || sl[0].TraceURL != "https://jaeger.example.com/trace/"+sl[0].TraceID {
		t.Errorf("slowest %+v", sl)
	}
	hook.mu.Lock()
	for i, ev := range hook.events {
		if !hook.sigOK[i] {
			t.Errorf("event %s: signature did not verify", ev.Type)
		}
		if ev.Type == "run.target_failed" {
			if ev.Run == nil || ev.Run.ID != rid || ev.Run.Verdict != "fail" || len(ev.Run.FailedTargets) != 1 || ev.Run.URL != "https://stampede.example.com/runs/"+rid || ev.Run.Project != "Shop" || ev.Run.Summary == nil {
				t.Errorf("target_failed event %+v", ev.Run)
			}
		}
	}
	hook.mu.Unlock()

	var deliveries []map[string]any
	if code := c.do("GET", "/notifications/channels/"+chID+"/deliveries", nil, &deliveries); code != 200 || len(deliveries) < 4 {
		t.Errorf("deliveries: %d %v", code, deliveries)
	}

	// A kill sends run.killed naming who pulled the switch.
	long := strings.Replace(yaml, "duration: 2s", "duration: 5m", 1)
	c.do("POST", "/scenarios/"+sc["id"].(string)+"/versions", map[string]string{"yaml": long}, nil)
	c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &run)
	time.Sleep(500 * time.Millisecond)
	c.do("POST", "/runs/"+run["id"].(string)+"/kill", nil, nil)
	waitFor(t, "run.killed", func() bool { return strings.Contains(strings.Join(hook.types(), ","), "run.killed") })
	hook.mu.Lock()
	for _, ev := range hook.events {
		if ev.Type == "run.killed" && (ev.Run == nil || ev.Run.KilledBy != "owner@acme.test" || ev.Run.Status != "aborted") {
			t.Errorf("killed event %+v", ev.Run)
		}
	}
	hook.mu.Unlock()

	// A server scenario cannot make the server fetch a URL of its choosing.
	direct := strings.Replace(strings.Replace(yaml, "integration: prom,", "url: 'http://169.254.169.254',", 1), "name: observed", "name: direct", 1)
	var sc2 map[string]any
	if code := c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": direct}, &sc2); code != 201 {
		t.Fatalf("scenario with url: %d", code)
	}
	if code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc2["id"], "targetId": tgt["id"]}, &e); code != 422 || !strings.Contains(e.Error.Message, "integration") {
		t.Errorf("run with observe.prometheus.url: %d %+v", code, e)
	}
	missing := strings.Replace(yaml, "integration: prom,", "integration: nope,", 1)
	c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": strings.Replace(missing, "name: observed", "name: missing", 1)}, &sc2)
	if code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc2["id"], "targetId": tgt["id"]}, &e); code != 422 || !strings.Contains(e.Error.Message, `no integration named "nope"`) {
		t.Errorf("run with a missing integration: %d %+v", code, e)
	}

	// Members who are not admins can list integrations but not change them
	// or see channels.
	if code := c.do("POST", "/users", map[string]string{"email": "ed@acme.test", "name": "Ed", "role": "editor", "password": "editor password 1"}, nil); code != 201 {
		t.Fatalf("create editor: %d", code)
	}
	ed := newClient(t, base)
	ed.do("POST", "/auth/login", map[string]string{"email": "ed@acme.test", "password": "editor password 1"}, nil)
	if code := ed.do("GET", "/integrations", nil, nil); code != 200 {
		t.Errorf("editor list integrations: %d", code)
	}
	for _, req := range []struct{ method, path string }{
		{"POST", "/integrations"}, {"DELETE", "/integrations/" + integ["id"].(string)},
		{"GET", "/notifications/channels"}, {"POST", "/notifications/channels/" + chID + "/test"},
	} {
		body := map[string]any{"name": "x", "kind": "traces", "url": "https://x/{traceId}"}
		if code := ed.do(req.method, req.path, body, nil); code != 403 {
			t.Errorf("editor %s %s: %d", req.method, req.path, code)
		}
	}

	if code := c.do("DELETE", "/notifications/channels/"+chID, nil, nil); code != 204 {
		t.Errorf("delete channel: %d", code)
	}
	if code := c.do("DELETE", "/integrations/"+integ["id"].(string), nil, nil); code != 204 {
		t.Errorf("delete integration: %d", code)
	}
}
