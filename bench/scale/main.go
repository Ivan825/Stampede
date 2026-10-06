// Command scale measures how much load one Stampede process can generate
// before the generator, not the target, becomes the limit. It starts the
// echo server as a separate process (fixed 1ms delay) and runs the engine
// at rising arrival rates. A level is clean when every planned iteration
// started (none dropped), the achieved rate is within 2% of the plan and
// the p99 scheduling lag stays under 10ms. The result is the highest clean
// rate on this machine, with its CPU count, so figures from different
// machines can be compared.
//
//	go run ./bench/scale                     # 5s per level
//	go run ./bench/scale -levels 1000,5000,20000 -hold 10s -out scale.json
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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Ivan825/Stampede/internal/runner"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/version"
)

type level struct {
	Rate        int     `json:"rate"`
	Achieved    float64 `json:"achievedRps"`
	Dropped     uint64  `json:"dropped"`
	Errors      float64 `json:"errorRate"`
	SchedLagP99 float64 `json:"schedLagP99Ms"`
	P99         float64 `json:"latencyP99Ms"`
	Clean       bool    `json:"clean"`
}

type result struct {
	Stampede string    `json:"stampede"`
	Date     time.Time `json:"date"`
	GOOS     string    `json:"goos"`
	GOARCH   string    `json:"goarch"`
	CPUs     int       `json:"cpus"`
	Hold     string    `json:"hold"`
	Levels   []level   `json:"levels"`
	// MaxCleanRPS is the highest clean level.
	MaxCleanRPS int `json:"maxCleanRps"`
}

func main() {
	levelsFlag := flag.String("levels", "1000,2000,5000,10000,20000,40000,80000", "arrival rates to try, per second")
	hold := flag.Duration("hold", 5*time.Second, "how long each level runs")
	out := flag.String("out", "", "write JSON results here")
	flag.Parse()

	dir, err := os.MkdirTemp("", "stampede-scale")
	if err != nil {
		fatal(err)
	}
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, "echoserver")
	if b, err := exec.Command("go", "build", "-o", bin, "github.com/Ivan825/Stampede/bench/echoserver").CombinedOutput(); err != nil {
		fatal(fmt.Errorf("build echo server: %w\n%s", err, b))
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	srv := exec.Command(bin, "-addr", addr, "-dist", "fixed:1ms")
	if err := srv.Start(); err != nil {
		fatal(err)
	}
	defer func() { _ = srv.Process.Kill(); _ = srv.Wait() }()
	for range 500 {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	res := result{Stampede: version.Version, Date: time.Now().UTC(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, CPUs: runtime.NumCPU(), Hold: hold.String()}
	fmt.Printf("one Stampede process on %d CPUs, %s per level, target: echo server with 1ms delay\n\n", res.CPUs, hold)
	fmt.Printf("  %8s %12s %9s %8s %14s %10s\n", "rate/s", "achieved/s", "dropped", "errors", "sched lag p99", "p99")
	for _, f := range strings.Split(*levelsFlag, ",") {
		rate, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || rate <= 0 {
			fatal(fmt.Errorf("bad level %q", f))
		}
		l, err := runLevel(addr, rate, *hold)
		if err != nil {
			fatal(err)
		}
		res.Levels = append(res.Levels, l)
		mark := "✓"
		if !l.Clean {
			mark = "✗"
		}
		fmt.Printf("%s %8d %12.0f %9d %7.2f%% %12.2fms %8.1fms\n", mark, l.Rate, l.Achieved, l.Dropped, l.Errors*100, l.SchedLagP99, l.P99)
		if l.Clean {
			res.MaxCleanRPS = l.Rate
		} else {
			break
		}
	}
	fmt.Printf("\nhighest clean rate: %d/s\n", res.MaxCleanRPS)
	if *out != "" {
		b, _ := json.MarshalIndent(res, "", "  ")
		if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
			fatal(err)
		}
	}
}

func runLevel(addr string, rate int, hold time.Duration) (level, error) {
	s, err := scenario.Parse(fmt.Appendf(nil, `
metadata: {name: scale}
target: {baseURL: "http://%s", http: {connections: shared}}
journeys: [{name: echo, steps: [{get: /echo}]}]
load: {mode: rate, rate: %d/s, duration: %s, gracefulStop: 5s}`, addr, rate, hold))
	if err != nil {
		return level{}, err
	}
	rep, err := runner.Run(context.Background(), runner.Options{Scenario: s, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		return level{}, err
	}
	lag := 0.0
	for _, p := range rep.Timeline {
		lag = math.Max(lag, p.SchedLag99)
	}
	l := level{Rate: rate, Achieved: rep.Overall.RPS, Dropped: rep.Overall.Dropped, Errors: rep.Overall.ErrorRate,
		SchedLagP99: lag * 1000, P99: rep.Overall.Latency.P99 * 1000}
	l.Clean = l.Dropped == 0 && l.Errors < 0.001 && l.Achieved >= 0.98*float64(rate) && l.SchedLagP99 < 10
	return l, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
