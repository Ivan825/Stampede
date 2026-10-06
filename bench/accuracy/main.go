// Command accuracy measures how closely Stampede's reported latency
// percentiles match ground truth. For each case it starts the calibrated
// echo server, drives it with Stampede's engine at a fixed arrival rate,
// and compares Stampede's p50, p95 and p99 with the exact hold times the
// server recorded. Network and HTTP overhead on loopback is part of the
// error, so the tolerance is the larger of 2% and 1ms.
//
//	go run ./bench/accuracy                   # all cases, ~2 minutes
//	go run ./bench/accuracy -quick            # short cases for CI
//	go run ./bench/accuracy -out results.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Ivan825/Stampede/bench/echo"
	"github.com/Ivan825/Stampede/internal/runner"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/version"
)

type benchCase struct {
	Name     string `json:"name"`
	Dist     string `json:"dist"`
	Rate     int    `json:"rate"`
	Duration string `json:"duration"`
}

var cases = []benchCase{
	// cases are filtered by -only when set
	{"fixed 10ms", "fixed:10ms", 200, "20s"},
	{"uniform 5-50ms", "uniform:5ms:50ms", 300, "20s"},
	{"lognormal median 20ms", "lognormal:20ms:0.6", 300, "30s"},
	{"bimodal 5ms / 300ms (2%)", "bimodal:5ms:300ms:0.02", 300, "30s"},
	{"high rate 2ms", "fixed:2ms", 2000, "20s"},
}

type quantileResult struct {
	Quantile string  `json:"quantile"`
	Truth    float64 `json:"truthMs"`
	Stampede float64 `json:"stampedeMs"`
	ErrorMs  float64 `json:"errorMs"`
	ErrorPct float64 `json:"errorPct"`
	Pass     bool    `json:"pass"`
}

type caseResult struct {
	benchCase
	Requests  uint64           `json:"requests"`
	Truth     uint64           `json:"serverCount"`
	Dropped   uint64           `json:"dropped"`
	Quantiles []quantileResult `json:"quantiles"`
	Pass      bool             `json:"pass"`
}

type results struct {
	Stampede string       `json:"stampede"`
	Date     time.Time    `json:"date"`
	GOOS     string       `json:"goos"`
	GOARCH   string       `json:"goarch"`
	CPUs     int          `json:"cpus"`
	Rule     string       `json:"rule"`
	Cases    []caseResult `json:"cases"`
	Pass     bool         `json:"pass"`
}

func main() {
	quick := flag.Bool("quick", false, "run short cases (for CI)")
	out := flag.String("out", "", "write JSON results to this file")
	only := flag.String("only", "", "run only cases whose name contains this text")
	flag.Parse()

	dir, err := os.MkdirTemp("", "stampede-accuracy")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	serverBin = filepath.Join(dir, "echoserver")
	if out, err := exec.Command("go", "build", "-o", serverBin, "github.com/Ivan825/Stampede/bench/echoserver").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build echo server: %v\n%s", err, out)
		os.Exit(1)
	}

	res := results{
		Stampede: version.Version, Date: time.Now().UTC(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		CPUs: runtime.NumCPU(), Rule: "|stampede - truth| <= max(2% of truth, 1ms)", Pass: true,
	}
	for _, c := range cases {
		if *only != "" && !strings.Contains(c.Name, *only) {
			continue
		}
		if *quick {
			c.Duration = "5s"
		}
		r, err := runCase(c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", c.Name, err)
			os.Exit(1)
		}
		res.Cases = append(res.Cases, r)
		res.Pass = res.Pass && r.Pass
		printCase(r)
	}
	if *out != "" {
		b, _ := json.MarshalIndent(res, "", "  ")
		if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if !res.Pass {
		fmt.Println("\nFAIL: at least one percentile is outside tolerance")
		os.Exit(2)
	}
	fmt.Println("\nPASS: every percentile is within tolerance")
}

var serverBin string

// startServer runs the echo server as a separate process so it does not
// share a Go runtime with the load generator, as a real target would not.
func startServer(dist string) (addr string, stop func(), err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	addr = ln.Addr().String()
	ln.Close()
	cmd := exec.Command(serverBin, "-addr", addr, "-dist", dist, "-seed", "42")
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}
	stop = func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return addr, stop, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	return "", nil, fmt.Errorf("echo server did not start")
}

func serverStats(addr string) (echo.Stats, error) {
	var st echo.Stats
	resp, err := http.Get("http://" + addr + "/stats")
	if err != nil {
		return st, err
	}
	defer resp.Body.Close()
	return st, json.NewDecoder(resp.Body).Decode(&st)
}

func runCase(c benchCase) (caseResult, error) {
	addr, stop, err := startServer(c.Dist)
	if err != nil {
		return caseResult{}, err
	}
	defer stop()

	s, err := scenario.Parse([]byte(fmt.Sprintf(`
metadata: {name: accuracy}
target: {baseURL: "http://%s"}
journeys: [{name: echo, steps: [{get: /echo}]}]
load: {mode: rate, rate: %d/s, duration: %s, maxVUs: 5000}`, addr, c.Rate, c.Duration)))
	if err != nil {
		return caseResult{}, err
	}
	rep, err := runner.Run(context.Background(), runner.Options{
		Scenario: s, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		return caseResult{}, err
	}
	truth, err := serverStats(addr)
	if err != nil {
		return caseResult{}, err
	}
	r := caseResult{benchCase: c, Requests: rep.Overall.Requests, Truth: uint64(truth.Count), Dropped: rep.Overall.Dropped, Pass: true}
	add := func(name string, t, got float64) {
		errMs := math.Abs(got-t) * 1000
		tol := math.Max(0.02*t*1000, 1)
		q := quantileResult{Quantile: name, Truth: t * 1000, Stampede: got * 1000, ErrorMs: errMs, Pass: errMs <= tol}
		if t > 0 {
			q.ErrorPct = errMs / (t * 1000) * 100
		}
		r.Quantiles = append(r.Quantiles, q)
		r.Pass = r.Pass && q.Pass
	}
	lat := rep.Overall.Latency
	add("p50", truth.P50, lat.P50)
	add("p95", truth.P95, lat.P95)
	add("p99", truth.P99, lat.P99)
	if os.Getenv("ACCURACY_DEBUG") != "" {
		sv := rep.Overall.Service
		var lag float64
		for _, p := range rep.Timeline {
			lag = math.Max(lag, p.SchedLag99)
		}
		fmt.Printf("  debug: service p95 %.2fms p99 %.2fms, max per-second sched-lag p99 %.3fms\n", sv.P95*1e3, sv.P99*1e3, lag*1e3)
	}
	if r.Dropped > 0 || r.Requests != r.Truth {
		r.Pass = false
	}
	return r, nil
}

func printCase(r caseResult) {
	status := "pass"
	if !r.Pass {
		status = "FAIL"
	}
	fmt.Printf("\n%s  %s  (%d/s for %s, %d requests, server saw %d, dropped %d)\n",
		status, r.Name, r.Rate, r.Duration, r.Requests, r.Truth, r.Dropped)
	for _, q := range r.Quantiles {
		mark := "✓"
		if !q.Pass {
			mark = "✗"
		}
		fmt.Printf("  %s %-4s truth %8.2fms  stampede %8.2fms  error %6.2fms (%5.2f%%)\n",
			mark, q.Quantile, q.Truth, q.Stampede, q.ErrorMs, q.ErrorPct)
	}
}
