package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/scenario"
)

type runOut struct {
	res   *Result
	total *metrics.Snapshot
	snaps []*metrics.Snapshot
	prog  *scenario.Program
}

func run(t *testing.T, src string, mut func(*Options)) runOut {
	t.Helper()
	s, err := scenario.Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	prog, err := scenario.Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.Load.Plan()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	out := runOut{total: metrics.NewSnapshot(0), prog: prog}
	opts := Options{
		Program: prog, Plan: plan, RunID: "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnSnapshot: func(sn *metrics.Snapshot) {
			mu.Lock()
			out.snaps = append(out.snaps, sn)
			out.total.Merge(sn)
			mu.Unlock()
		},
	}
	if mut != nil {
		mut(&opts)
	}
	e, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	out.res, err = e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestClosedModel(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(5 * time.Millisecond)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()
	out := run(t, fmt.Sprintf(`
metadata: {name: closed}
target: {baseURL: %q}
journeys:
  - name: a
    steps:
      - get: /one
      - get: /two
load: {vus: 4, duration: 1s, gracefulStop: 2s}`, srv.URL), nil)

	tot := out.total.Totals()
	if tot.Requests == 0 || tot.Failed != 0 {
		t.Fatalf("requests=%d failed=%d", tot.Requests, tot.Failed)
	}
	if int64(tot.Requests) != hits.Load() {
		t.Errorf("recorded %d requests, server saw %d", tot.Requests, hits.Load())
	}
	// ~4 VUs * 1s / 10ms per iteration => ~400 requests; allow wide margin.
	if tot.Requests < 200 || tot.Requests > 900 {
		t.Errorf("unexpected request count %d", tot.Requests)
	}
	if p := tot.Service.Quantile(0.5); p < 5000 {
		t.Errorf("median service time %dµs below the 5ms server delay", p)
	}
	if out.res.PeakVUs != 4 || out.res.StopReason != StopCompleted {
		t.Errorf("peak=%d reason=%s", out.res.PeakVUs, out.res.StopReason)
	}
	if out.total.Journeys[0].Completed == 0 {
		t.Error("no completed iterations")
	}
}

func TestOpenModelRateIsExact(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()
	out := run(t, fmt.Sprintf(`
metadata: {name: open}
target: {baseURL: %q}
journeys: [{name: a, steps: [{get: /}]}]
load: {mode: rate, rate: 200/s, duration: 2s}`, srv.URL), nil)
	started := out.total.Journeys[0].Started
	if started != 400 {
		t.Errorf("started %d iterations, want exactly 400", started)
	}
	if out.total.Dropped != 0 {
		t.Errorf("dropped %d", out.total.Dropped)
	}
	// A sanity bound only: shared CI runners, busy with other packages'
	// tests (and their Chrome), stall for tens of milliseconds. Pacing
	// precision is measured by the accuracy job on a quiet machine.
	lag := out.total.SchedLag.Quantile(0.99)
	switch {
	case runtime.GOOS == "darwin" && os.Getenv("CI") != "":
		// GitHub's macOS runners stall for 20 to 70ms under load; the
		// iteration count above is what this test is about.
		t.Logf("p99 scheduling lag %dµs (not checked on macOS CI)", lag)
	case lag > 50000:
		t.Errorf("p99 scheduling lag %dµs is too high", lag)
	}
}

func TestOpenModelDropsWhenNoUserFree(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()
	out := run(t, fmt.Sprintf(`
metadata: {name: drops}
target: {baseURL: %q}
journeys: [{name: a, steps: [{get: /}]}]
load: {mode: rate, rate: 100/s, duration: 1s, maxVUs: 5}`, srv.URL), nil)
	if out.total.Dropped < 50 {
		t.Errorf("expected many dropped iterations with 5 users at 100/s and 300ms latency, got %d", out.total.Dropped)
	}
}

func TestArrivalScheduleRamp(t *testing.T) {
	p := &scenario.Plan{Start: 0, Stages: []scenario.PlanStage{
		{Duration: 10 * time.Second, Target: 100},
		{Duration: 5 * time.Second, Target: 100},
		{Duration: 5 * time.Second, Target: 0},
	}}
	s := newArrivalSchedule(p)
	n := 0
	var last time.Duration
	for {
		at, ok := s.next()
		if !ok {
			break
		}
		if at < last {
			t.Fatalf("arrival times went backwards at %d", n)
		}
		last = at
		n++
	}
	// 0->100 over 10s = 500, hold 100 for 5s = 500, 100->0 over 5s = 250.
	if n != 1250 {
		t.Errorf("got %d arrivals, want 1250", n)
	}
}

func TestExtractChecksAndFeeders(t *testing.T) {
	var badAuth atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("X-Session", "s-"+body["email"])
		fmt.Fprintf(w, `{"token":"tok-%s","items":[{"id":7}]}`, body["email"])
	})
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer tok-") || r.PathValue("id") != "7" {
			badAuth.Add(1)
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("traceparent") == "" {
			badAuth.Add(1)
		}
		fmt.Fprint(w, `<html><h1 class="t"> Seven </h1></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	csv := "email,password\n"
	for i := 0; i < 20; i++ {
		csv += fmt.Sprintf("u%d@x.test,pw\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "users.csv"), []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`
metadata: {name: flow}
target: {baseURL: %q}
data:
  users: {csv: %q, mode: unique}
journeys:
  - name: login
    steps:
      - post: /login
        json: {email: "${data.users.email}", password: "${data.users.password}"}
        check: {status: 200, json: {"$.items[0].id": 7}}
        extract: {token: "$.token", id: "$.items[0].id", session: "header:X-Session"}
      - get: /items/${id}
        headers: {Authorization: "Bearer ${token}"}
        check: {bodyContains: Seven, expr: "status == 200 && body.contains('h1')"}
        extract: {title: "css:h1.t"}
      - get: /items/${id}
        if: ${title != 'Seven'}
load: {iterations: 20, vus: 4}`, srv.URL, filepath.Join(dir, "users.csv"))
	out := run(t, src, nil)
	tot := out.total.Totals()
	if badAuth.Load() != 0 {
		t.Errorf("%d requests had wrong auth or missing traceparent", badAuth.Load())
	}
	// 20 iterations x 2 requests (third step skipped by its condition).
	if tot.Requests != 40 || tot.Failed != 0 {
		t.Errorf("requests=%d failed=%d errors=%v", tot.Requests, tot.Failed, tot.Errors)
	}
	if tot.ChecksPassed != 80 {
		t.Errorf("checks passed = %d, want 80", tot.ChecksPassed)
	}
}

func TestUniqueDataExhaustionStops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	out := run(t, fmt.Sprintf(`
metadata: {name: exhaust}
target: {baseURL: %q}
data: {ids: {range: [1, 10], mode: unique}}
journeys: [{name: a, steps: [{get: "/${data.ids}"}]}]
load: {vus: 2, duration: 5s}`, srv.URL), nil)
	if got := out.total.Totals().Requests; got != 10 {
		t.Errorf("requests = %d, want 10", got)
	}
	if out.res.StopReason != "test data exhausted" {
		t.Errorf("stop reason = %q", out.res.StopReason)
	}
}

func TestFailuresAbortIteration(t *testing.T) {
	var second atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	mux.HandleFunc("/next", func(w http.ResponseWriter, r *http.Request) { second.Add(1) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	out := run(t, fmt.Sprintf(`
metadata: {name: fail}
target: {baseURL: %q}
journeys: [{name: a, steps: [{get: /fail}, {get: /next}]}]
load: {iterations: 10}`, srv.URL), nil)
	tot := out.total.Totals()
	if tot.Failed != 10 || tot.Errors["HTTP 503"] != 10 || second.Load() != 0 {
		t.Errorf("failed=%d errors=%v second=%d", tot.Failed, tot.Errors, second.Load())
	}
	if out.total.Journeys[0].Failed != 10 {
		t.Errorf("journey failures = %d", out.total.Journeys[0].Failed)
	}
}

func TestConnectionRefusedClassified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	out := run(t, fmt.Sprintf(`
metadata: {name: refused}
target: {baseURL: %q}
journeys: [{name: a, steps: [{get: /}]}]
load: {iterations: 3}`, url), nil)
	if n := out.total.Totals().Errors["connection refused"]; n != 3 {
		t.Errorf("connection refused = %d, errors=%v", n, out.total.Totals().Errors)
	}
}

func TestSharesPartitionLoad(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	src := fmt.Sprintf(`
metadata: {name: share}
target: {baseURL: %q}
journeys: [{name: a, steps: [{get: /}]}]
load: {mode: rate, rate: 300/s, duration: 1s}`, srv.URL)
	var total uint64
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, sh := range [][2]float64{{0, 0.25}, {0.25, 0.7}, {0.7, 1}} {
		wg.Add(1)
		go func(lo, hi float64) {
			defer wg.Done()
			out := run(t, src, func(o *Options) { o.ShareLo, o.ShareHi = lo, hi })
			mu.Lock()
			total += out.total.Journeys[0].Started
			mu.Unlock()
		}(sh[0], sh[1])
	}
	wg.Wait()
	if total != 300 {
		t.Errorf("three shares started %d iterations, want exactly 300", total)
	}
}

func TestAllowHostBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	out := run(t, fmt.Sprintf(`
metadata: {name: blocked}
target: {baseURL: %q}
journeys: [{name: a, steps: [{get: "https://example.com/"}]}]
load: {iterations: 2}`, srv.URL), func(o *Options) {
		o.AllowHost = func(u *url.URL) bool { return u.Host != "example.com" }
	})
	if n := out.total.Totals().Errors["blocked by safety"]; n != 2 {
		t.Errorf("blocked = %d", n)
	}
}

func TestNetworkEmulationAddsLatency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	out := run(t, fmt.Sprintf(`
metadata: {name: netem}
target:
  baseURL: %q
  network: {rtt: 80ms}
journeys: [{name: a, steps: [{get: /}]}]
load: {iterations: 4}`, srv.URL), nil)
	tot := out.total.Totals()
	if tot.Requests != 4 || tot.Failed != 0 {
		t.Fatalf("requests %d failed %d", tot.Requests, tot.Failed)
	}
	// Every request pays at least one emulated round trip.
	if p := tot.Latency.Min(); p < 80_000 {
		t.Errorf("fastest request %dµs, want at least 80ms", p)
	}
}
