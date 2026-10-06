package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Ivan825/Stampede/internal/report"
)

// TestRunQueriesPrometheusAndLinksTraces runs a scenario with an observe
// block against an httptest target and a fake Prometheus, and checks the
// JSON report carries the target's metrics and trace links.
func TestRunQueriesPrometheusAndLinksTraces(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") }))
	defer target.Close()
	var promCalls atomic.Int64
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		promCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer tok-123" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = r.ParseForm()
		start, _ := strconv.ParseFloat(r.Form.Get("start"), 64)
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[%f,"0.5"],[%f,"0.75"]]}]}}`, start, start+1)
	}))
	defer prom.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "s.yaml")
	src := `
metadata: {name: observed}
target: {baseURL: "${env.TARGET}"}
journeys: [{name: j, steps: [{get: /}]}]
load: {vus: 1, duration: 1s}
observe:
  prometheus:
    url: ${env.PROM}
    bearerToken: ${secret.PROM_TOKEN}
    queries: {cpu: 'rate(process_cpu_seconds_total[30s])'}
  traces: {url: "https://jaeger.example.com/trace/{traceId}"}
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(dir, "r.json")
	t.Setenv("STAMPEDE_SECRET_PROM_TOKEN", "tok-123")
	var stdout bytes.Buffer
	err := runScenario(context.Background(), &stdout, io.Discard, path, &runFlags{
		json: jsonPath, quiet: true, repeat: 1, pause: "0s",
		env: []string{"TARGET=" + target.URL, "PROM=" + prom.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rep, err := report.ReadJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.TargetMetrics) != 1 || rep.TargetMetrics[0].Error != "" || len(rep.TargetMetrics[0].Points) != 2 {
		b, _ := json.Marshal(rep.TargetMetrics)
		t.Fatalf("target metrics = %s (prometheus called %d times)", b, promCalls.Load())
	}
	slow := rep.Journeys[0].Steps[0].Slowest
	if len(slow) == 0 || !strings.HasPrefix(slow[0].TraceURL, "https://jaeger.example.com/trace/") || len(slow[0].TraceID) != 32 {
		t.Errorf("slowest = %+v", slow)
	}
	if !strings.Contains(stdout.String(), "target metrics (Prometheus)") {
		t.Errorf("summary lacks target metrics:\n%s", stdout.String())
	}
}

func TestRunRefusesServerIntegrations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.yaml")
	src := `
metadata: {name: observed}
target: {baseURL: "http://127.0.0.1:1"}
journeys: [{name: j, steps: [{get: /}]}]
load: {vus: 1, duration: 1s}
observe: {prometheus: {integration: prod, queries: {cpu: up}}}
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runScenario(context.Background(), io.Discard, io.Discard, path, &runFlags{quiet: true, repeat: 1, pause: "0s"})
	if err == nil || !strings.Contains(err.Error(), "names a server integration") {
		t.Errorf("error = %v", err)
	}
}
