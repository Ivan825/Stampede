package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/scenario"
)

func buildSlow(t *testing.T) *Report {
	t.Helper()
	s, err := scenario.Parse([]byte(`
metadata: {name: slow}
target: {baseURL: "http://localhost"}
journeys:
  - name: shop
    steps: [{get: /a}, {get: /b}]
load: {vus: 2, duration: 2s}`))
	if err != nil {
		t.Fatal(err)
	}
	prog, _ := scenario.Compile(s)
	plan, _ := s.Load.Plan()
	t0 := time.Unix(1_700_000_000, 0)
	var snaps []*metrics.Snapshot
	for i := int64(0); i < 2; i++ {
		sn := metrics.NewSnapshot(i)
		for n := 0; n < 20; n++ {
			start := t0.Add(time.Duration(i)*time.Second + time.Duration(n)*time.Millisecond)
			sm := &metrics.Sample{Step: 1, Start: start, End: start.Add(time.Duration(10+n+int(i)*100) * time.Millisecond), Status: 200}
			sm.TraceID[0], sm.TraceID[1] = byte(i), byte(n)
			sn.Step(1).Add(sm)
		}
		snaps = append(snaps, sn)
	}
	return Build(Input{Program: prog, Plan: plan, Started: t0, Ended: t0.Add(2 * time.Second), StopReason: "completed", Snapshots: snaps})
}

func TestSlowestInReport(t *testing.T) {
	r := buildSlow(t)
	steps := r.Journeys[0].Steps
	if len(steps[0].Slowest) != 0 {
		t.Errorf("a step with no requests has slow requests: %+v", steps[0].Slowest)
	}
	sl := steps[1].Slowest
	if len(sl) != metrics.MaxSlowest {
		t.Fatalf("got %d slow requests, want %d", len(sl), metrics.MaxSlowest)
	}
	// The slowest of the run come from the second interval: 129ms at t+1.019s.
	if sl[0].Latency != 0.129 || sl[0].T < 1.018 || sl[0].T > 1.02 || sl[0].TraceID != "0113"+strings.Repeat("0", 28) {
		t.Errorf("slowest = %+v", sl[0])
	}
	r.SetTraceLinks("https://jaeger.example.com/trace/{traceId}")
	if got := r.Journeys[0].Steps[1].Slowest[0].TraceURL; got != "https://jaeger.example.com/trace/0113"+strings.Repeat("0", 28) {
		t.Errorf("trace url = %q", got)
	}

	var html, text, js bytes.Buffer
	if err := r.WriteHTML(&html); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html.String(), `<a href="https://jaeger.example.com/trace/0113`) || !strings.Contains(html.String(), "Slowest requests") {
		t.Error("HTML report lacks the slowest requests with trace links")
	}
	r.WriteText(&text)
	if !strings.Contains(text.String(), "slowest requests") || !strings.Contains(text.String(), "https://jaeger.example.com/trace/0113") {
		t.Errorf("terminal summary lacks slow requests:\n%s", text.String())
	}
	if err := r.WriteJSON(&js); err != nil {
		t.Fatal(err)
	}
	back, err := ReadJSON(&js)
	if err != nil {
		t.Fatal(err)
	}
	if back.Journeys[0].Steps[1].Slowest[0].TraceURL == "" {
		t.Error("JSON lost the trace link")
	}
}

func TestTargetMetricsInReport(t *testing.T) {
	r := buildSlow(t)
	r.TargetMetrics = []TargetMetric{
		{Name: "cpu", Query: `rate(process_cpu_seconds_total{job="shop"}[30s])`, Points: []MetricPoint{{0, 0.2}, {1, 0.95}, {2, 0.5}}},
		{Name: "memory", Query: "process_resident_memory_bytes", Points: []MetricPoint{{0, 120e6}, {2, 180e6}}},
		{Name: "broken", Query: "up(", Error: "prometheus: parse error"},
	}
	var text, html bytes.Buffer
	r.WriteText(&text)
	for _, want := range []string{"target metrics", "cpu", "0.2", "0.95", "0.5", "120M", "180M", "broken", "no data", "parse error"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("terminal summary lacks %q:\n%s", want, text.String())
		}
	}
	if err := r.WriteHTML(&html); err != nil {
		t.Fatal(err)
	}
	h := html.String()
	if !strings.Contains(h, "Target metrics") || strings.Count(h, `aria-label="cpu"`) != 1 || !strings.Contains(h, "rate(process_cpu_seconds_total{job=&#34;shop&#34;}[30s])") {
		t.Error("HTML report lacks the target metric charts")
	}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"targetMetrics":[{"name":"cpu"`) {
		t.Errorf("JSON lacks targetMetrics: %s", b)
	}
}

func TestMetricValue(t *testing.T) {
	for in, want := range map[float64]string{0: "0", 0.5: "0.5", 12.345: "12.3", 1234: "1234", 45600: "45.6k", 120e6: "120M", 2.5e9: "2.5G", 0.0001234: "1.23e-04", -3: "-3"} {
		if got := MetricValue(in); got != want {
			t.Errorf("MetricValue(%v) = %q, want %q", in, got, want)
		}
	}
}
