package server_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/ai/provider"
)

func TestRunNarrative(t *testing.T) {
	fakes := &aiFakes{}
	base, _, _ := startAIServer(t, fakes)
	c := newClient(t, base)
	setup(t, c)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"ok":true}`) }))
	t.Cleanup(target.Close)

	var proj, tgt, sc, run map[string]any
	c.do("POST", "/projects", map[string]string{"name": "Shop"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "local", "baseURL": target.URL}, &tgt)
	yaml := `
metadata: {name: narrated}
target: {baseURL: "http://ignored.example"}
journeys: [{name: home, steps: [{get: /}]}]
load: {mode: rate, rate: 20/s, duration: 2s}
targets: ["http.p95 < 1s"]`
	if code := c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": yaml}, &sc); code != 201 {
		t.Fatalf("scenario: %d", code)
	}
	if code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &run); code != 201 {
		t.Fatalf("run: %d", code)
	}
	rid := run["id"].(string)

	var e errBody
	// Before the run finishes there is no report to summarise.
	if code := c.do("POST", "/runs/"+rid+"/narrative", nil, &e); code != 404 && code != 409 {
		t.Errorf("narrative before the report: %d", code)
	}
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

	if code := c.do("POST", "/runs/"+rid+"/narrative", nil, &e); code != 409 || !strings.Contains(e.Error.Message, "no AI provider") {
		t.Fatalf("narrative without a provider: %d %+v", code, e)
	}
	if code := c.do("POST", "/ai/providers", map[string]any{"kind": "ollama", "model": "llama3", "monthlyTokenCap": 2000}, nil); code != 201 {
		t.Fatalf("provider: %d", code)
	}
	reply := `{"summary":"The run met its latency target.","claims":[
	  {"text":"The p95 latency target passed.","label":"measured","refs":["target.0","overall.latency"]},
	  {"text":"Every request succeeded, which suggests the target has headroom at this load.","label":"suspected","refs":["overall.requests"]},
	  {"text":"The database is the bottleneck.","label":"measured","refs":["db.cpu"]}]}`
	fakes.set(&provider.Fake{Replies: []any{reply, reply}, PerCall: provider.Usage{InputTokens: 600, OutputTokens: 150}})

	var res struct {
		Narrative struct {
			Summary string `json:"summary"`
			Claims  []struct {
				Text  string   `json:"text"`
				Label string   `json:"label"`
				Refs  []string `json:"refs"`
			} `json:"claims"`
			Facts []struct {
				ID string `json:"id"`
			} `json:"facts"`
		} `json:"narrative"`
		Usage struct {
			InputTokens int64 `json:"inputTokens"`
		} `json:"usage"`
	}
	if code := c.do("POST", "/runs/"+rid+"/narrative", nil, &res); code != 200 {
		t.Fatalf("narrative: %d", code)
	}
	// The claim citing an unknown fact is dropped (after one repair round).
	if res.Narrative.Summary == "" || len(res.Narrative.Claims) != 2 || len(res.Narrative.Facts) != 3 || res.Usage.InputTokens != 1200 {
		t.Fatalf("narrative = %+v", res)
	}

	// Saved into the report and its downloads.
	var rep map[string]any
	c.do("GET", "/runs/"+rid+"/report", nil, &rep)
	if n, ok := rep["narrative"].(map[string]any); !ok || n["summary"] != "The run met its latency target." {
		t.Errorf("report narrative = %v", rep["narrative"])
	}
	req, _ := http.NewRequest("GET", base+"/api/v1/runs/"+rid+"/report?format=html", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(html), "The p95 latency target passed.") || !strings.Contains(string(html), `class="label suspected"`) {
		t.Error("the HTML report must show the narrative")
	}

	// Tokens count towards the monthly cap.
	var provs []map[string]any
	c.do("GET", "/ai/providers", nil, &provs)
	if provs[0]["usedTokensThisMonth"].(float64) != 1500 {
		t.Errorf("used tokens = %v", provs[0]["usedTokensThisMonth"])
	}
	fakes.set(&provider.Fake{Replies: []any{reply}, PerCall: provider.Usage{InputTokens: 600, OutputTokens: 150}})
	if code := c.do("POST", "/runs/"+rid+"/narrative", nil, nil); code != 200 {
		t.Fatalf("second narrative: %d", code)
	}
	if code := c.do("POST", "/runs/"+rid+"/narrative", nil, &e); code != 429 || e.Error.Code != "ai_token_cap" {
		t.Errorf("over the cap: %d %+v", code, e)
	}

	// Viewers cannot spend tokens.
	c.do("POST", "/users", map[string]string{"email": "v@acme.test", "name": "V", "role": "viewer", "password": "viewer password 1"}, nil)
	v := newClient(t, base)
	v.do("POST", "/auth/login", map[string]string{"email": "v@acme.test", "password": "viewer password 1"}, nil)
	if code := v.do("POST", "/runs/"+rid+"/narrative", nil, nil); code != 403 {
		t.Errorf("viewer: %d", code)
	}
}
