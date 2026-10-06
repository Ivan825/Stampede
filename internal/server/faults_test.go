package server_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/agent"
)

func TestRunWithFaults(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") }))
	t.Cleanup(target.Close)
	proxy := agent.NewProxy("api", "127.0.0.1:0", strings.TrimPrefix(target.URL, "http://"), quiet)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := proxy.Start(ctx); err != nil {
		t.Fatal(err)
	}
	ag := agent.New(agent.Config{Proxies: []*agent.Proxy{proxy}, Logger: quiet})
	agentSrv := httptest.NewServer(ag.Handler("agent-token-0123456789"))
	t.Cleanup(agentSrv.Close)

	base := startServer(t)
	c := newClient(t, base)
	setup(t, c)
	var proj, tgt map[string]any
	c.do("POST", "/projects", map[string]string{"name": "Shop"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "via agent", "baseURL": "http://" + proxy.Addr()}, &tgt)

	var e errBody
	if code := c.do("POST", "/integrations", map[string]any{"name": "chaos", "kind": "agent", "url": agentSrv.URL}, &e); code != 422 || !strings.Contains(e.Error.Message, "bearerToken") {
		t.Errorf("agent without a token: %d %+v", code, e)
	}
	if code := c.do("POST", "/integrations", map[string]any{"name": "chaos", "kind": "agent", "url": agentSrv.URL, "bearerToken": "agent-token-0123456789"}, nil); code != 201 {
		t.Fatalf("integration: %d", code)
	}

	n := 0
	start := func(faults string) (int, map[string]any, errBody) {
		var sc, run map[string]any
		var e errBody
		n++
		yaml := fmt.Sprintf("\nmetadata: {name: faults-%d}", n) + `
target: {baseURL: "http://ignored.example"}
journeys: [{name: home, steps: [{get: /}]}]
load: {mode: rate, rate: 30/s, duration: 3s}
` + faults
		if code := c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": yaml}, &sc); code != 201 {
			t.Fatalf("scenario: %d", code)
		}
		code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &run)
		if code != 201 {
			c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &e)
		}
		return code, run, e
	}

	// A URL in the scenario is refused on the server.
	if code, _, e := start(`faults: {agent: {url: "` + agentSrv.URL + `", token: x}, timeline: [{at: 1s, for: 1s, proxy: api, latency: 50ms}]}`); code != 422 || !strings.Contains(e.Error.Message, "integration") {
		t.Errorf("url on the server: %d %+v", code, e)
	}
	// A proxy the agent does not have is refused before the run exists.
	if code, _, e := start(`faults: {agent: {integration: chaos}, timeline: [{at: 1s, for: 1s, proxy: db, latency: 50ms}]}`); code != 422 || !strings.Contains(e.Error.Message, `no proxy "db"`) {
		t.Errorf("missing proxy: %d %+v", code, e)
	}

	code, run, _ := start(`faults: {agent: {integration: chaos}, timeline: [{name: slow api, at: 1s, for: 1s, proxy: api, latency: 80ms}]}`)
	if code != 201 {
		t.Fatalf("run: %d", code)
	}
	rid := run["id"].(string)
	deadline := time.Now().Add(30 * time.Second)
	for {
		var got map[string]any
		c.do("GET", "/runs/"+rid, nil, &got)
		if got["status"] == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish: %v", got)
		}
		time.Sleep(200 * time.Millisecond)
	}
	var rep struct {
		Faults []struct {
			Label, Kind, Target, Error string
			Start, End                 float64
		} `json:"faults"`
		Timeline []struct {
			T   float64 `json:"t"`
			P50 float64 `json:"p50"`
		} `json:"timeline"`
	}
	c.do("GET", "/runs/"+rid+"/report", nil, &rep)
	if len(rep.Faults) != 1 || rep.Faults[0].Error != "" || rep.Faults[0].Label != "slow api" || rep.Faults[0].Start < 0.8 || rep.Faults[0].Start > 1.5 {
		t.Fatalf("faults = %+v", rep.Faults)
	}
	slow := false
	for _, p := range rep.Timeline {
		if p.T == 1 && p.P50 > 0.12 {
			slow = true
		}
	}
	if !slow {
		t.Errorf("second 1 should show the injected latency: %+v", rep.Timeline)
	}
	if len(ag.Active()) != 0 || !proxy.Fault().IsZero() {
		t.Error("the run's faults must be cleared when it ends")
	}
}
