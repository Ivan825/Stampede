package coordinator

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/scenario"
)

// checkTiling asserts that shares tile [0, 1) exactly: the first starts at
// 0, each starts where the previous ended, the last ends at 1.
func checkTiling(t *testing.T, sh []share) {
	t.Helper()
	if sh[0].lo != 0 {
		t.Errorf("first share starts at %v", sh[0].lo)
	}
	for i := 1; i < len(sh); i++ {
		if sh[i].lo != sh[i-1].hi {
			t.Errorf("share %d starts at %v, previous ended at %v", i, sh[i].lo, sh[i-1].hi)
		}
	}
	if sh[len(sh)-1].hi != 1 {
		t.Errorf("last share ends at %v, want exactly 1", sh[len(sh)-1].hi)
	}
	for i, s := range sh {
		if !(s.hi > s.lo) {
			t.Errorf("share %d is empty: %+v", i, s)
		}
	}
}

func TestComputeShares(t *testing.T) {
	tests := []struct {
		name    string
		weights []float64
	}{
		{"one", []float64{4}},
		{"equal", []float64{2, 2, 2}},
		{"proportional", []float64{1, 2, 5}},
		{"thirds rounding", []float64{1, 1, 1, 1, 1, 1, 1}},
		{"uneven", []float64{0.3, 7, 1e-3, 64}},
		{"many", func() []float64 {
			w := make([]float64, 997)
			for i := range w {
				w[i] = float64(1 + i%13)
			}
			return w
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sh, err := computeShares(tt.weights)
			if err != nil {
				t.Fatal(err)
			}
			checkTiling(t, sh)
			var total float64
			for _, w := range tt.weights {
				total += w
			}
			for i, s := range sh {
				want := tt.weights[i] / total
				if got := s.hi - s.lo; math.Abs(got-want) > 1e-12 {
					t.Errorf("share %d = %v, want %v", i, got, want)
				}
			}
		})
	}
}

func TestComputeSharesRejectsBadWeights(t *testing.T) {
	for _, w := range [][]float64{nil, {1, 0}, {1, -2}, {math.NaN()}, {math.Inf(1)}} {
		if _, err := computeShares(w); err == nil {
			t.Errorf("weights %v accepted", w)
		}
	}
}

func cands(spec ...string) []candidate {
	// spec entries: "name/region/cpus/maxVUs"
	var out []candidate
	for _, s := range spec {
		var c candidate
		var cpus float64
		parts := strings.Split(s, "/")
		c.name, c.id, c.region = parts[0], parts[0], parts[1]
		fmt.Sscan(parts[2], &cpus)
		c.cpus = cpus
		if len(parts) > 3 {
			fmt.Sscan(parts[3], &c.maxVUs)
		}
		out = append(out, c)
	}
	return out
}

func TestSelectWorkers(t *testing.T) {
	vus := &scenario.Plan{Executor: scenario.ExecConstantVUs, Value: 100}
	tests := []struct {
		name    string
		avail   []candidate
		n       int
		regions map[string]float64
		plan    *scenario.Plan
		want    map[string]float64 // worker -> share size
		err     string
	}{
		{
			name: "all by cpus", avail: cands("a//2", "b//6", "c//8"), plan: vus,
			want: map[string]float64{"a": 2.0 / 16, "b": 6.0 / 16, "c": 8.0 / 16},
		},
		{
			name: "largest n", avail: cands("a//2", "b//6", "c//8"), n: 2, plan: vus,
			want: map[string]float64{"b": 6.0 / 14, "c": 8.0 / 14},
		},
		{name: "too few", avail: cands("a//2"), n: 2, plan: vus, err: "2 workers requested but only 1"},
		{name: "none", plan: vus, err: "no workers"},
		{
			name: "regions", avail: cands("m1/mumbai/4", "m2/mumbai/4", "f1/frankfurt/2"), plan: vus,
			regions: map[string]float64{"mumbai": 1, "frankfurt": 1},
			want:    map[string]float64{"m1": 0.25, "m2": 0.25, "f1": 0.5},
		},
		{
			name: "regions with count", avail: cands("m1/mumbai/4", "m2/mumbai/2", "m3/mumbai/1", "f1/frankfurt/2", "f2/frankfurt/2"), plan: vus,
			n: 3, regions: map[string]float64{"mumbai": 0.75, "frankfurt": 0.25},
			want: map[string]float64{"m1": 0.75 * 4 / 6, "m2": 0.75 * 2 / 6, "f1": 0.25},
		},
		{
			name: "empty region", avail: cands("m1/mumbai/4"), plan: vus,
			regions: map[string]float64{"mumbai": 1, "virginia": 1}, err: "no idle workers in region virginia",
		},
		{
			name: "vu cap", avail: cands("a//1/10", "b//1/100"), plan: vus,
			err: "a would run 50 virtual users but accepts 10",
		},
		{
			name: "vu cap fits", avail: cands("a//1/50", "b//1/50"), plan: vus,
			want: map[string]float64{"a": 0.5, "b": 0.5},
		},
		{
			name: "rate plan checks maxVUs", avail: cands("a//1/10"),
			plan: &scenario.Plan{Executor: scenario.ExecConstantRate, MaxVUs: 11},
			err:  "accepts 10",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel, err := selectWorkers(tt.avail, tt.n, tt.regions, tt.plan)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			checkTiling(t, sel.shares)
			if len(sel.workers) != len(tt.want) {
				t.Fatalf("chose %d workers, want %d", len(sel.workers), len(tt.want))
			}
			for i, w := range sel.workers {
				want, ok := tt.want[w.name]
				if !ok {
					t.Errorf("unexpected worker %s", w.name)
					continue
				}
				if got := sel.shares[i].hi - sel.shares[i].lo; math.Abs(got-want) > 1e-12 {
					t.Errorf("%s share %v, want %v", w.name, got, want)
				}
			}
		})
	}
}

func TestRegionCountsLargestRemainder(t *testing.T) {
	by := map[string][]candidate{
		"a": cands("a1/a/1", "a2/a/1", "a3/a/1", "a4/a/1", "a5/a/1"),
		"b": cands("b1/b/1", "b2/b/1", "b3/b/1"),
		"c": cands("c1/c/1"),
	}
	regions := map[string]float64{"a": 0.6, "b": 0.3, "c": 0.1}
	got, err := regionCounts([]string{"a", "b", "c"}, regions, 1, 7, by)
	if err != nil {
		t.Fatal(err)
	}
	// 7 * (.6, .3, .1) = 4.2, 2.1, 0.7 -> 4, 2, 1 (c gets at least one).
	if got["a"] != 4 || got["b"] != 2 || got["c"] != 1 {
		t.Errorf("counts %v", got)
	}
}
