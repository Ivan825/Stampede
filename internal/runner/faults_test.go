package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/agent"
	"github.com/Ivan825/Stampede/internal/scenario"
)

func agentFor(t *testing.T, upstream string) (*agent.Agent, *agent.Proxy, *agent.Client) {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := agent.NewProxy("api", "127.0.0.1:0", upstream, quiet)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	a := agent.New(agent.Config{Proxies: []*agent.Proxy{p}, Logger: quiet})
	srv := httptest.NewServer(a.Handler("tok-0123456789abcdef"))
	t.Cleanup(srv.Close)
	return a, p, &agent.Client{BaseURL: srv.URL, Token: "tok-0123456789abcdef"}
}

func TestRunInjectsFaultsOnTime(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") }))
	defer target.Close()
	a, p, client := agentFor(t, strings.TrimPrefix(target.URL, "http://"))

	s, err := scenario.Parse(fmt.Appendf(nil, `
metadata: {name: faults}
target: {baseURL: "http://%s"}
journeys: [{name: home, steps: [{get: /}]}]
load: {mode: rate, rate: 40/s, duration: 5s}
faults:
  agent: {url: "%s", token: x}
  timeline:
    - {name: slow api, at: 1s, for: 2500ms, proxy: api, latency: 100ms}`, p.Addr(), client.BaseURL))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), Options{
		Scenario: s, RunID: "run-1", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Faults: &FaultPlan{Client: client, Steps: s.Faults.Timeline},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Faults) != 1 {
		t.Fatalf("faults = %+v", rep.Faults)
	}
	f := rep.Faults[0]
	if f.Error != "" || f.Label != "slow api" || f.Kind != "proxy" || f.Target != "api" || f.Start < 0.9 || f.Start > 1.5 || f.End-f.Start < 2.4 {
		t.Errorf("event = %+v", f)
	}
	// The fault covers all of second 2 (and most of 1 and 3); seconds 0
	// and 4 are clear of it.
	for _, pt := range rep.Timeline {
		switch {
		case pt.T == 0 || pt.T >= 4:
			if pt.P50 > 0.05 {
				t.Errorf("second %v: p50 %.3fs outside the fault", pt.T, pt.P50)
			}
		case pt.T == 2:
			if pt.P50 < 0.15 {
				t.Errorf("second %v: p50 %.3fs inside the fault", pt.T, pt.P50)
			}
		}
	}
	if len(a.Active()) != 0 || !p.Fault().IsZero() {
		t.Error("every fault must be cleared when the run ends")
	}
	var html strings.Builder
	if err := rep.WriteHTML(&html); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html.String(), `class="band fault"`) || !strings.Contains(html.String(), "Injected faults") {
		t.Error("the HTML report must shade and list the fault")
	}
}

func TestFaultPlanCheckFailsBeforeLoad(t *testing.T) {
	var hits int
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer target.Close()
	_, _, client := agentFor(t, strings.TrimPrefix(target.URL, "http://"))
	s, err := scenario.Parse(fmt.Appendf(nil, `
metadata: {name: faults}
target: {baseURL: "%s"}
journeys: [{name: home, steps: [{get: /}]}]
load: {mode: rate, rate: 10/s, duration: 1s}
faults:
  agent: {url: "http://agent", token: x}
  timeline:
    - {at: 0s, for: 1s, proxy: database, reset: true}
    - {at: 0s, for: 1s, container: shop, action: pause}`, target.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Run(context.Background(), Options{Scenario: s, Faults: &FaultPlan{Client: client, Steps: s.Faults.Timeline}})
	if err == nil || !strings.Contains(err.Error(), `no proxy "database" (it has: api)`) || !strings.Contains(err.Error(), "--allow-container") {
		t.Fatalf("err = %v", err)
	}
	if hits != 0 {
		t.Errorf("no load may be sent when the faults cannot be injected; target saw %d requests", hits)
	}
	bad := &FaultPlan{Client: &agent.Client{BaseURL: client.BaseURL, Token: "wrong"}, Steps: s.Faults.Timeline}
	if err := bad.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("wrong token: %v", err)
	}
}
