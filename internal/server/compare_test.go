package server_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type comparison struct {
	A struct {
		Label string
		Runs  []string
	} `json:"a"`
	B struct {
		Label string
		Runs  []string
	} `json:"b"`
	Comparable bool     `json:"comparable"`
	Problems   []string `json:"problems"`
	Verdict    string   `json:"verdict"`
	Confidence float64  `json:"confidence"`
	Markdown   string   `json:"markdown"`
	Metrics    []struct {
		Name    string    `json:"name"`
		A       []float64 `json:"a"`
		B       []float64 `json:"b"`
		Change  *float64  `json:"change"`
		CILow   *float64  `json:"ciLow"`
		CIHigh  *float64  `json:"ciHigh"`
		Noise   float64   `json:"noiseFloor"`
		Verdict string    `json:"verdict"`
	} `json:"metrics"`
	Steps []struct {
		Journey string `json:"journey"`
		Step    string `json:"step"`
		Metrics []struct {
			Name    string `json:"name"`
			Verdict string `json:"verdict"`
		} `json:"metrics"`
	} `json:"steps"`
}

func TestCompareRuns(t *testing.T) {
	var slow atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if slow.Load() {
			time.Sleep(40 * time.Millisecond)
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	t.Cleanup(target.Close)

	base := startServer(t)
	c := newClient(t, base)
	setup(t, c)
	var proj, tgt, sc map[string]any
	c.do("POST", "/projects", map[string]string{"name": "Shop"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "local", "baseURL": target.URL}, &tgt)
	yaml := `
metadata: {name: compared}
journeys: [{name: home, steps: [{get: /}]}]
load: {mode: rate, rate: 20/s, duration: 2s}`
	if code := c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": yaml}, &sc); code != 201 {
		t.Fatalf("scenario: %d", code)
	}

	start := func(scenarioYAML string) string {
		t.Helper()
		if scenarioYAML != "" {
			c.do("POST", "/scenarios/"+sc["id"].(string)+"/versions", map[string]string{"yaml": scenarioYAML}, nil)
		}
		var run map[string]any
		if code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &run); code != 201 {
			t.Fatalf("run: %d %v", code, run)
		}
		return run["id"].(string)
	}
	finish := func(id string) {
		t.Helper()
		eventually(t, 60*time.Second, "run "+id+" to finish", func() bool {
			var got map[string]any
			c.do("GET", "/runs/"+id, nil, &got)
			return got["status"] == "completed"
		})
	}
	var a, b []string
	for range 2 {
		id := start("")
		finish(id)
		a = append(a, id)
	}
	slow.Store(true)
	for range 2 {
		id := start("")
		finish(id)
		b = append(b, id)
	}

	// A viewer may compare; labels are used in the Markdown.
	viewer := member(t, c, base, "viewer@acme.test", "viewer")
	var cmp comparison
	if code := viewer.do("POST", "/compare", map[string]any{"a": a, "b": b, "labelA": "before", "labelB": "after"}, &cmp); code != 200 {
		t.Fatalf("compare: %d %+v", code, cmp)
	}
	if !cmp.Comparable || len(cmp.Problems) != 0 {
		t.Errorf("runs of one scenario should be comparable: %v", cmp.Problems)
	}
	if cmp.A.Label != "before" || cmp.B.Label != "after" || strings.Join(cmp.A.Runs, ",") != strings.Join(a, ",") {
		t.Errorf("sides: %+v %+v", cmp.A, cmp.B)
	}
	if cmp.Verdict != "regression" || cmp.Confidence != 0.95 {
		t.Errorf("verdict %s confidence %v", cmp.Verdict, cmp.Confidence)
	}
	var p95Found bool
	for _, m := range cmp.Metrics {
		if m.Name != "p95" {
			continue
		}
		p95Found = true
		if len(m.A) != 2 || len(m.B) != 2 || m.Verdict != "regression" || m.Change == nil || *m.Change <= 0 || m.CILow == nil || *m.CILow <= 0 || m.Noise < 0.02 {
			t.Errorf("p95: %+v", m)
		}
	}
	if !p95Found {
		t.Errorf("no p95 row: %+v", cmp.Metrics)
	}
	if len(cmp.Steps) != 1 || cmp.Steps[0].Journey != "home" || len(cmp.Steps[0].Metrics) != 2 || cmp.Steps[0].Metrics[0].Verdict != "regression" {
		t.Errorf("steps: %+v", cmp.Steps)
	}
	if !strings.Contains(cmp.Markdown, "Stampede comparison: regression") || !strings.Contains(cmp.Markdown, "| before | after |") || !strings.Contains(cmp.Markdown, "| p95 |") {
		t.Errorf("markdown:\n%s", cmp.Markdown)
	}

	// Default labels, and a different load plan is flagged not comparable.
	other := strings.Replace(yaml, "rate: 20/s", "rate: 10/s", 1)
	slow.Store(false)
	odd := start(other)
	finish(odd)
	cmp = comparison{}
	if code := c.do("POST", "/compare", map[string]any{"a": a, "b": []string{odd}}, &cmp); code != 200 {
		t.Fatalf("compare different plans: %d", code)
	}
	if cmp.Comparable || cmp.A.Label != "A" || cmp.B.Label != "B" || !strings.Contains(strings.Join(cmp.Problems, ";"), "different load plans") {
		t.Errorf("different plans: comparable=%v labels=%s/%s problems=%v", cmp.Comparable, cmp.A.Label, cmp.B.Label, cmp.Problems)
	}

	// Unknown, repeated and unfinished runs are refused with the reasons.
	long := strings.Replace(yaml, "duration: 2s", "duration: 1m", 1)
	running := start(long)
	t.Cleanup(func() { c.do("POST", "/runs/"+running+"/kill", nil, nil) })
	var e errBody
	unknown := "00000000-0000-4000-8000-000000000000"
	if code := c.do("POST", "/compare", map[string]any{"a": []string{a[0], a[0]}, "b": []string{running, unknown}}, &e); code != 422 {
		t.Fatalf("invalid runs: %d %+v", code, e)
	}
	details := strings.Join(e.Error.Details, "\n")
	for _, want := range []string{"listed more than once", "has not finished", unknown + " not found"} {
		if !strings.Contains(details, want) {
			t.Errorf("details missing %q:\n%s", want, details)
		}
	}
	if code := c.do("POST", "/compare", map[string]any{"a": []string{}, "b": b}, nil); code < 400 || code >= 500 {
		t.Errorf("empty side: %d", code)
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = a[0]
	}
	if code := c.do("POST", "/compare", map[string]any{"a": many, "b": b}, nil); code < 400 || code >= 500 {
		t.Errorf("21 runs: %d", code)
	}
	if code := newClient(t, base).do("POST", "/compare", map[string]any{"a": a, "b": b}, nil); code != 401 {
		t.Errorf("anonymous: %d", code)
	}
}
