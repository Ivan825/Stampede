// Command compare runs the same open-model workload through Stampede, k6
// and wrk2 against the calibrated echo server, and compares each tool's
// p50, p95 and p99 with the server's exact hold times.
//
// The tools do not all measure the same thing, and the table says which:
//
//   - Stampede latency and wrk2 count from each request's scheduled send
//     time, so queueing in the generator is included (coordinated
//     omission corrected).
//   - Stampede service time and k6's http_req_duration count from the
//     moment the request was actually sent.
//
// On an unsaturated generator the two agree; the gap between them grows
// when a generator falls behind its schedule.
//
//	go run ./bench/compare                          # tools found in PATH
//	go run ./bench/compare -k6 /path/k6 -wrk2 /path/wrk -out compare.json
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"regexp"
	"runtime"
	"strconv"
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
	{"fixed 10ms", "fixed:10ms", 200, "20s"},
	{"lognormal median 20ms", "lognormal:20ms:0.6", 300, "30s"},
	{"bimodal 5ms / 300ms (2%)", "bimodal:5ms:300ms:0.02", 300, "30s"},
}

// Percentiles are in milliseconds.
type Percentiles struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

type toolResult struct {
	Tool string `json:"tool"`
	// Measures is "scheduled" or "sent": where the clock starts.
	Measures string       `json:"measures"`
	Version  string       `json:"version,omitempty"`
	Ran      bool         `json:"ran"`
	Note     string       `json:"note,omitempty"`
	Requests uint64       `json:"requests,omitempty"`
	Latency  *Percentiles `json:"latency,omitempty"`
	// Truth is the echo server's exact percentiles for this tool's run.
	Truth *Percentiles `json:"truth,omitempty"`
	// Within is whether every percentile is within max(2%, 1ms) of truth.
	Within bool `json:"withinTolerance"`
}

type caseResult struct {
	benchCase
	Tools []toolResult `json:"tools"`
}

type results struct {
	Stampede string       `json:"stampede"`
	Date     time.Time    `json:"date"`
	GOOS     string       `json:"goos"`
	GOARCH   string       `json:"goarch"`
	CPUs     int          `json:"cpus"`
	Rule     string       `json:"rule"`
	Cases    []caseResult `json:"cases"`
}

var (
	serverBin string
	k6Bin     string
	wrkBin    string
)

func main() {
	quick := flag.Bool("quick", false, "5-second cases")
	out := flag.String("out", "", "write JSON results to this file")
	only := flag.String("only", "", "run only cases whose name contains this text")
	flag.StringVar(&k6Bin, "k6", "k6", "k6 binary (skipped when not found)")
	flag.StringVar(&wrkBin, "wrk2", "wrk2", "wrk2 binary (skipped when not found; wrk2 builds as \"wrk\")")
	flag.Parse()

	dir, err := os.MkdirTemp("", "stampede-compare")
	if err != nil {
		fatal(err)
	}
	defer os.RemoveAll(dir)
	serverBin = filepath.Join(dir, "echoserver")
	if b, err := exec.Command("go", "build", "-o", serverBin, "github.com/Ivan825/Stampede/bench/echoserver").CombinedOutput(); err != nil {
		fatal(fmt.Errorf("build echo server: %w\n%s", err, b))
	}
	res := results{Stampede: version.Version, Date: time.Now().UTC(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		CPUs: runtime.NumCPU(), Rule: "|tool - truth| <= max(2% of truth, 1ms)"}
	for _, c := range cases {
		if *only != "" && !strings.Contains(c.Name, *only) {
			continue
		}
		if *quick {
			c.Duration = "5s"
		}
		r, err := runCase(c, dir)
		if err != nil {
			fatal(fmt.Errorf("%s: %w", c.Name, err))
		}
		printCase(r)
		res.Cases = append(res.Cases, r)
	}
	if *out != "" {
		b, _ := json.MarshalIndent(res, "", "  ")
		if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
			fatal(err)
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func startServer(dist string) (string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	addr := ln.Addr().String()
	ln.Close()
	cmd := exec.Command(serverBin, "-addr", addr, "-dist", dist, "-seed", "42")
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}
	stop := func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }
	for range 500 {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return addr, stop, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	return "", nil, errors.New("echo server did not start")
}

func truthAndReset(addr string) (Percentiles, int64, error) {
	resp, err := http.Get("http://" + addr + "/stats")
	if err != nil {
		return Percentiles{}, 0, err
	}
	var st echo.Stats
	err = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if err != nil {
		return Percentiles{}, 0, err
	}
	r, err := http.Post("http://"+addr+"/reset", "", nil)
	if err == nil {
		r.Body.Close()
	}
	return Percentiles{st.P50 * 1e3, st.P95 * 1e3, st.P99 * 1e3}, int64(st.Count), err
}

func within(p, t Percentiles) bool {
	ok := func(a, b float64) bool { return math.Abs(a-b) <= math.Max(0.02*b, 1) }
	return ok(p.P50, t.P50) && ok(p.P95, t.P95) && ok(p.P99, t.P99)
}

// runCase runs each tool in turn against a fresh echo server. Each tool
// gets its own ground truth, read and reset after it finishes, since the
// sampled delays differ from run to run.
func runCase(c benchCase, dir string) (caseResult, error) {
	addr, stop, err := startServer(c.Dist)
	if err != nil {
		return caseResult{}, err
	}
	defer stop()
	r := caseResult{benchCase: c}

	// Stampede.
	s, err := scenario.Parse(fmt.Appendf(nil, `
metadata: {name: compare}
target: {baseURL: "http://%s"}
journeys: [{name: echo, steps: [{get: /echo}]}]
load: {mode: rate, rate: %d/s, duration: %s, maxVUs: 5000}`, addr, c.Rate, c.Duration))
	if err != nil {
		return r, err
	}
	rep, err := runner.Run(context.Background(), runner.Options{Scenario: s, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		return r, err
	}
	truth, _, err := truthAndReset(addr)
	if err != nil {
		return r, err
	}
	lat := Percentiles{rep.Overall.Latency.P50 * 1e3, rep.Overall.Latency.P95 * 1e3, rep.Overall.Latency.P99 * 1e3}
	svc := Percentiles{rep.Overall.Service.P50 * 1e3, rep.Overall.Service.P95 * 1e3, rep.Overall.Service.P99 * 1e3}
	r.Tools = append(r.Tools,
		toolResult{Tool: "stampede", Measures: "scheduled", Version: version.Version, Ran: true, Requests: rep.Overall.Requests, Latency: &lat, Truth: &truth, Within: within(lat, truth)},
		toolResult{Tool: "stampede (service time)", Measures: "sent", Version: version.Version, Ran: true, Requests: rep.Overall.Requests, Latency: &svc, Truth: &truth, Within: within(svc, truth)})

	r.Tools = append(r.Tools, runK6(c, addr, dir), runWrk2(c, addr))
	return r, nil
}

func lookup(bin string) (string, bool) {
	p, err := exec.LookPath(bin)
	return p, err == nil
}

func toolVersion(bin string, args ...string) string {
	out, _ := exec.Command(bin, args...).CombinedOutput()
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return line
}

func runK6(c benchCase, addr, dir string) toolResult {
	t := toolResult{Tool: "k6", Measures: "sent"}
	bin, ok := lookup(k6Bin)
	if !ok {
		t.Note = "k6 not installed"
		return t
	}
	t.Version = toolVersion(bin, "version")
	script := filepath.Join(dir, "k6.js")
	summary := filepath.Join(dir, "k6-summary.json")
	js := fmt.Sprintf(`import http from 'k6/http';
export const options = {
  discardResponseBodies: true,
  summaryTrendStats: ['p(50)', 'p(95)', 'p(99)', 'count'],
  scenarios: { echo: { executor: 'constant-arrival-rate', rate: %d, timeUnit: '1s', duration: '%s', preAllocatedVUs: 200, maxVUs: 5000 } },
};
export default function () { http.get('http://%s/echo'); }
`, c.Rate, c.Duration, addr)
	if err := os.WriteFile(script, []byte(js), 0o644); err != nil {
		t.Note = err.Error()
		return t
	}
	cmd := exec.Command(bin, "run", "--quiet", "--no-usage-report", "--summary-export", summary, script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Note = "k6 failed: " + lastLine(stderr.String(), err)
		return t
	}
	var sum struct {
		Metrics map[string]map[string]float64 `json:"metrics"`
	}
	b, err := os.ReadFile(summary)
	if err == nil {
		err = json.Unmarshal(b, &sum)
	}
	m := sum.Metrics["http_req_duration"]
	if err != nil || m == nil {
		t.Note = "could not read the k6 summary"
		return t
	}
	truth, _, err := truthAndReset(addr)
	if err != nil {
		t.Note = err.Error()
		return t
	}
	t.Ran = true
	t.Requests = uint64(sum.Metrics["http_reqs"]["count"])
	t.Latency = &Percentiles{m["p(50)"], m["p(95)"], m["p(99)"]}
	t.Within = within(*t.Latency, truth)
	t.Truth = &truth
	return t
}

// wrk2's summary lists 50, 75, 90, 99, 99.9... percent but not 95, so p95
// comes from the detailed spectrum that follows it, whose rows are
// "value(ms) percentile count 1/(1-percentile)".
var (
	wrkLine     = regexp.MustCompile(`^\s*(50|99)\.000%\s+([\d.]+)(us|ms|s|m)\s*$`)
	wrkSpectrum = regexp.MustCompile(`^\s*([\d.]+)\s+([\d.]+)\s+\d+\s+(?:[\d.]+|inf)\s*$`)
)

// parseWrk2 reads p50, p95 and p99 in milliseconds from wrk2 --latency output.
func parseWrk2(out []byte) (Percentiles, bool) {
	var p Percentiles
	var have50, have95, have99 bool
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if m := wrkLine.FindStringSubmatch(line); m != nil {
			v, _ := strconv.ParseFloat(m[2], 64)
			switch m[3] {
			case "us":
				v /= 1000
			case "s":
				v *= 1000
			case "m":
				v *= 60000
			}
			if m[1] == "50" {
				p.P50, have50 = v, true
			} else {
				p.P99, have99 = v, true
			}
			continue
		}
		if m := wrkSpectrum.FindStringSubmatch(line); m != nil && !have95 {
			if q, _ := strconv.ParseFloat(m[2], 64); q >= 0.95 {
				p.P95, _ = strconv.ParseFloat(m[1], 64)
				have95 = true
			}
		}
	}
	return p, have50 && have95 && have99
}

func runWrk2(c benchCase, addr string) toolResult {
	t := toolResult{Tool: "wrk2", Measures: "scheduled"}
	bin, ok := lookup(wrkBin)
	if !ok {
		t.Note = "wrk2 not installed"
		return t
	}
	t.Version = "wrk2"
	// wrk2 gives every connection its own fixed schedule (rate/conns per
	// second), so a slow reply delays the requests queued behind it on that
	// connection and wrk2 rightly counts the wait. At three requests a
	// second per connection the gap (333 ms) is longer than the slowest
	// reply in these cases (300 ms), so none is delayed, as with k6's and
	// Stampede's pools.
	conns := max(c.Rate/3, 16)
	d, err := time.ParseDuration(c.Duration)
	if err != nil {
		t.Note = err.Error()
		return t
	}
	ctx, cancel := context.WithTimeout(context.Background(), d+2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-t2", "-c"+strconv.Itoa(conns), "-d"+c.Duration, "-R"+strconv.Itoa(c.Rate), "--latency", "http://"+addr+"/echo")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Note = fmt.Sprintf("wrk2 did not finish within %s; last output: %s", d+2*time.Minute, lastLine(string(out), ctx.Err()))
		return t
	}
	if err != nil {
		t.Note = "wrk2 failed: " + lastLine(string(out), err)
		return t
	}
	p, ok := parseWrk2(out)
	if !ok {
		t.Note = "could not read wrk2's latency distribution (is this wrk rather than wrk2?)"
		return t
	}
	truth, n, err := truthAndReset(addr)
	if err != nil {
		t.Note = err.Error()
		return t
	}
	t.Ran, t.Latency, t.Requests = true, &p, uint64(n)
	t.Within = within(p, truth)
	t.Truth = &truth
	return t
}

func lastLine(s string, err error) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return err.Error()
	}
	return s
}

func printCase(r caseResult) {
	fmt.Printf("\n%s  (%d/s for %s)\n", r.Name, r.Rate, r.Duration)
	fmt.Printf("  %-26s %-10s %9s %9s %9s\n", "", "clock from", "p50 ms", "p95 ms", "p99 ms")
	for _, t := range r.Tools {
		if !t.Ran {
			fmt.Printf("  %-26s %s\n", t.Tool, t.Note)
			continue
		}
		mark := "✓"
		if !t.Within {
			mark = "✗"
		}
		fmt.Printf("%s %-26s %-10s %9.2f %9.2f %9.2f\n", mark+" ", t.Tool, t.Measures, t.Latency.P50, t.Latency.P95, t.Latency.P99)
		fmt.Printf("   %-26s %-10s %9.2f %9.2f %9.2f\n", "  truth for that run", "", t.Truth.P50, t.Truth.P95, t.Truth.P99)
	}
}
