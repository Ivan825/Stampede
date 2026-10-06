package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/scenario"
)

func build(t *testing.T, latency time.Duration, failEvery int) *Report {
	t.Helper()
	s, err := scenario.Parse([]byte(`
metadata: {name: rep}
target: {baseURL: "http://localhost"}
journeys:
  - name: shop
    steps: [{get: /a}, {get: /b}]
load: {vus: 2, duration: 3s}
targets: ["http.p95 < 100ms", "errors < 5%", "shop/GET /b.p99 < 200ms"]`))
	if err != nil {
		t.Fatal(err)
	}
	prog, _ := scenario.Compile(s)
	plan, _ := s.Load.Plan()
	t0 := time.Unix(1_700_000_000, 0)
	var snaps []*metrics.Snapshot
	for i := int64(0); i < 3; i++ {
		sn := metrics.NewSnapshot(i)
		for n := 0; n < 100; n++ {
			for step := 0; step < 2; step++ {
				sm := &metrics.Sample{Step: step, Start: t0, End: t0.Add(latency), Status: 200}
				if failEvery > 0 && n%failEvery == 0 {
					sm.Failed, sm.Err, sm.Status = true, "HTTP 500", 500
				}
				sn.Step(step).Add(sm)
			}
			sn.Journey(0).Completed++
		}
		sn.VUs = 2
		snaps = append(snaps, sn)
	}
	return Build(Input{Program: prog, Plan: plan, Started: t0, Ended: t0.Add(3 * time.Second), StopReason: "completed", Snapshots: snaps})
}

func TestBuildPass(t *testing.T) {
	r := build(t, 20*time.Millisecond, 0)
	if r.Verdict != VerdictPass {
		t.Fatalf("verdict %s: %+v", r.Verdict, r.Thresholds)
	}
	if r.Overall.Requests != 600 || r.Overall.RPS != 200 || r.Overall.Iterations != 300 {
		t.Errorf("overall %+v", r.Overall)
	}
	if len(r.Timeline) != 3 || r.Timeline[0].RPS != 200 {
		t.Errorf("timeline %+v", r.Timeline)
	}
	if len(r.Journeys) != 1 || len(r.Journeys[0].Steps) != 2 {
		t.Fatalf("journeys %+v", r.Journeys)
	}
}

func TestBuildFail(t *testing.T) {
	r := build(t, 150*time.Millisecond, 10)
	if r.Verdict != VerdictFail {
		t.Fatalf("verdict %s", r.Verdict)
	}
	byName := map[string]Check{}
	for _, c := range r.Thresholds {
		byName[c.Source] = c
	}
	if byName["http.p95 < 100ms"].Pass || byName["errors < 5%"].Pass || !byName["shop/GET /b.p99 < 200ms"].Pass {
		t.Errorf("unexpected results: %+v", r.Thresholds)
	}
	if len(r.Errors) != 2 || r.Errors[0].Error != "HTTP 500" {
		t.Errorf("errors %+v", r.Errors)
	}
}

func TestExports(t *testing.T) {
	r := build(t, 150*time.Millisecond, 10)
	var buf bytes.Buffer
	if err := r.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := ReadJSON(&buf)
	if err != nil || back.Overall.Requests != r.Overall.Requests {
		t.Fatalf("json round trip: %v", err)
	}
	buf.Reset()
	if err := r.WriteHTML(&buf); err != nil {
		t.Fatal(err)
	}
	h := buf.String()
	for _, want := range []string{"<svg", "FAIL", "http.p95 &lt; 100ms", "GET /b"} {
		if !strings.Contains(h, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	if strings.Contains(h, "http://") && strings.Contains(h, "<script src") {
		t.Error("HTML report must not load external scripts")
	}
	buf.Reset()
	if err := r.WriteJUnit(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `failures="2"`) {
		t.Errorf("junit: %s", buf.String())
	}
	buf.Reset()
	r.WriteMarkdown(&buf)
	if !strings.Contains(buf.String(), "| `errors < 5%` |") {
		t.Errorf("markdown: %s", buf.String())
	}
	buf.Reset()
	r.WriteText(&buf)
	if !strings.Contains(buf.String(), "✗ http.p95 < 100ms") {
		t.Errorf("text: %s", buf.String())
	}
}

func TestNoDataIsNotPass(t *testing.T) {
	s, _ := scenario.Parse([]byte(`
metadata: {name: nodata}
target: {baseURL: "http://localhost"}
journeys: [{name: a, steps: [{get: /}]}]
load: {vus: 1, duration: 1s}
targets: ["p95 < 1s"]`))
	prog, _ := scenario.Compile(s)
	plan, _ := s.Load.Plan()
	r := Build(Input{Program: prog, Plan: plan})
	if r.Verdict != VerdictFail || r.Thresholds[0].ObservedText != "no data" {
		t.Errorf("a target with no data must fail: %+v", r.Thresholds)
	}
}
