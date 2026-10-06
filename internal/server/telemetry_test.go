package server_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestTracingAndMetrics checks the API and each run are traced, and that
// /metrics carries the API latency histogram the Grafana dashboard uses.
func TestTracingAndMetrics(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") }))
	defer target.Close()
	base := startServer(t)
	c := newClient(t, base)
	setup(t, c)
	var proj, tgt, sc, run map[string]any
	c.do("POST", "/projects", map[string]string{"name": "Traced"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "local", "baseURL": target.URL}, &tgt)
	c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": `
metadata: {name: traced}
journeys: [{name: j, steps: [{get: /}]}]
load: {vus: 1, duration: 1s}`}, &sc)
	if code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &run); code != 201 {
		t.Fatalf("run: %d", code)
	}
	waitFor(t, "the run span", func() bool {
		for _, s := range rec.Ended() {
			if s.Name() == "run" {
				return true
			}
		}
		return false
	})
	var runSpan sdktrace.ReadOnlySpan
	names := map[string]bool{}
	for _, s := range rec.Ended() {
		names[s.Name()] = true
		if s.Name() == "run" {
			runSpan = s
		}
	}
	if !names["POST /api/v1/projects/{projectId}/runs"] || !names["POST /api/v1/setup"] {
		t.Errorf("API spans should be named after routes, got %v", names)
	}
	attrs := map[string]string{}
	for _, a := range runSpan.Attributes() {
		attrs[string(a.Key)] = a.Value.String()
	}
	if attrs["stampede.run.id"] != run["id"] || attrs["stampede.run.status"] != "completed" || attrs["stampede.scenario"] != "traced" {
		t.Errorf("run span attributes %v", attrs)
	}
	if len(runSpan.Links()) != 1 || !runSpan.Links()[0].SpanContext.IsValid() {
		t.Error("the run span should link to the request that started it")
	}

	resp, err := http.Get(base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := string(b)
	for _, want := range []string{
		`stampede_http_request_duration_seconds_count{code="201",method="POST",route="/api/v1/projects/{projectId}/runs"} 1`,
		"stampede_runs_active", `stampede_runs_finished_total{status="completed"} 1`, "go_goroutines",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("/metrics lacks %s", want)
		}
	}

	// Every Stampede and Go runtime metric the Grafana dashboard queries
	// must exist. (process_* metrics depend on the operating system.)
	dash, err := os.ReadFile(filepath.Join("..", "..", "deploy", "grafana", "stampede-server.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(dash, &doc); err != nil {
		t.Fatalf("dashboard is not JSON: %v", err)
	}
	queried := regexp.MustCompile(`\b(stampede|go)_[a-z_]+`).FindAllString(string(dash), -1)
	if len(queried) < 5 {
		t.Fatalf("dashboard queries only %v", queried)
	}
	for _, n := range queried {
		if !strings.Contains(m, "\n"+n) {
			t.Errorf("the dashboard queries %s, which /metrics does not serve", n)
		}
	}
}
