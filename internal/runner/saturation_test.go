package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// One user cannot keep up with 40 arrivals a second against a slow
// target: iterations are dropped, the generator is saturated, and the
// failed target becomes generator-limited.
func TestLocalSaturationLimitsVerdict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
	}))
	defer srv.Close()
	s, err := scenario.Parse([]byte(fmt.Sprintf(`
metadata: {name: sat}
target: {baseURL: %q}
journeys: [{name: a, steps: [{get: /}]}]
load: {mode: rate, rate: 40/s, duration: 3s, maxVUs: 1, gracefulStop: 2s}
targets: ["http.p95 < 10ms"]`, srv.URL)))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), Options{Scenario: s})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != report.VerdictGeneratorLimit {
		t.Fatalf("verdict %s, notes %q", rep.Verdict, rep.Notes)
	}
	if len(rep.Workers) != 1 || len(rep.Workers[0].Saturated) == 0 || !strings.Contains(strings.Join(rep.Workers[0].SaturationReasons, ","), "iterations dropped") {
		t.Fatalf("workers %+v", rep.Workers)
	}
	if !strings.Contains(strings.Join(rep.Notes, "\n"), "this machine") {
		t.Errorf("notes %q", rep.Notes)
	}
}

func TestHealthyLocalRunListsNoWorkers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	s, _ := scenario.Parse([]byte(fmt.Sprintf(`
metadata: {name: ok}
target: {baseURL: %q}
journeys: [{name: a, steps: [{get: /}]}]
load: {iterations: 20, vus: 2}
targets: ["errors < 1%%"]`, srv.URL)))
	rep, err := Run(context.Background(), Options{Scenario: s})
	if err != nil || rep.Verdict != report.VerdictPass || len(rep.Workers) != 0 {
		t.Fatalf("err %v verdict %s workers %+v", err, rep.Verdict, rep.Workers)
	}
}
