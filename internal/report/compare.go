package report

import (
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
)

// Comparison judges whether version B differs from version A, using
// repeated runs of each. A single run is noisy, so a change is only called
// a regression or improvement when the bootstrap confidence interval of the
// difference excludes zero and the change is larger than the noise floor
// measured from the repeats themselves.
type Comparison struct {
	A          Side          `json:"a"`
	B          Side          `json:"b"`
	Comparable bool          `json:"comparable"`
	Problems   []string      `json:"problems,omitempty"`
	Metrics    []MetricDelta `json:"metrics"`
	Verdict    string        `json:"verdict"` // regression, improvement, no-change, inconclusive
	Confidence float64       `json:"confidence"`
}

// Side describes one version's runs.
type Side struct {
	Label string   `json:"label"`
	Runs  []string `json:"runs"`
}

// MetricDelta compares one metric.
type MetricDelta struct {
	Name string `json:"name"`
	// HigherIsBetter is true for throughput-like metrics.
	HigherIsBetter bool      `json:"higherIsBetter"`
	A              []float64 `json:"a"`
	B              []float64 `json:"b"`
	MeanA          float64   `json:"meanA"`
	MeanB          float64   `json:"meanB"`
	// Change is (meanB - meanA) / meanA.
	Change float64 `json:"change"`
	// CILow and CIHigh bound the relative change.
	CILow  float64 `json:"ciLow"`
	CIHigh float64 `json:"ciHigh"`
	// NoiseFloor is the relative spread between repeats of one version.
	NoiseFloor float64 `json:"noiseFloor"`
	Verdict    string  `json:"verdict"`
}

// Comparison verdicts.
const (
	CmpRegression   = "regression"
	CmpImprovement  = "improvement"
	CmpNoChange     = "no-change"
	CmpInconclusive = "inconclusive"
)

// minNoise is the smallest noise floor assumed, even when repeats agree
// closely: real systems rarely reproduce better than this.
const minNoise = 0.02

// minLatencyChange is the smallest absolute latency change (seconds) that
// can count, matching the engine's measured accuracy of about 1ms.
const minLatencyChange = 0.001

type metricDef struct {
	name   string
	higher bool
	get    func(*Report) (float64, bool)
}

var compareMetrics = []metricDef{
	{"p50", false, func(r *Report) (float64, bool) { return r.Overall.Latency.P50, r.Overall.Requests > 0 }},
	{"p95", false, func(r *Report) (float64, bool) { return r.Overall.Latency.P95, r.Overall.Requests > 0 }},
	{"p99", false, func(r *Report) (float64, bool) { return r.Overall.Latency.P99, r.Overall.Requests > 0 }},
	{"error rate", false, func(r *Report) (float64, bool) { return r.Overall.ErrorRate, r.Overall.Requests > 0 }},
	{"throughput", true, func(r *Report) (float64, bool) { return r.Overall.RPS, r.Overall.Requests > 0 }},
	{"max sustainable load", true, func(r *Report) (float64, bool) {
		if r.Breakpoint == nil {
			return 0, false
		}
		return r.Breakpoint.LastPass, true
	}},
}

// Compare compares runs of version A with runs of version B.
func Compare(a, b []*Report, labelA, labelB string) *Comparison {
	c := &Comparison{A: side(labelA, a), B: side(labelB, b), Comparable: true, Confidence: 0.95}
	c.Problems = comparable(append(append([]*Report{}, a...), b...))
	c.Comparable = len(c.Problems) == 0

	rng := rand.New(rand.NewPCG(1, 2)) // deterministic output for the same inputs
	worst := CmpNoChange
	anyImprove := false
	for _, m := range compareMetrics {
		va, vb := values(a, m), values(b, m)
		if len(va) == 0 || len(vb) == 0 {
			continue
		}
		d := MetricDelta{Name: m.name, HigherIsBetter: m.higher, A: va, B: vb, MeanA: mean(va), MeanB: mean(vb)}
		d.NoiseFloor = math.Max(minNoise, math.Max(spread(va), spread(vb)))
		d.Change = relChange(d.MeanA, d.MeanB)
		d.CILow, d.CIHigh = bootstrapCI(rng, va, vb, 0.95, 5000)
		d.Verdict = judge(d, len(va) >= 2 && len(vb) >= 2)
		switch d.Verdict {
		case CmpRegression:
			worst = CmpRegression
		case CmpImprovement:
			anyImprove = true
		case CmpInconclusive:
			if worst == CmpNoChange {
				worst = CmpInconclusive
			}
		}
		c.Metrics = append(c.Metrics, d)
	}
	c.Verdict = worst
	if worst == CmpNoChange && anyImprove {
		c.Verdict = CmpImprovement
	}
	if !c.Comparable && c.Verdict != CmpNoChange {
		c.Verdict = CmpInconclusive
	}
	return c
}

func side(label string, rs []*Report) Side {
	s := Side{Label: label}
	for _, r := range rs {
		id := r.RunID
		if id == "" {
			id = r.Started.Format("2006-01-02 15:04:05")
		}
		s.Runs = append(s.Runs, id)
	}
	return s
}

func values(rs []*Report, m metricDef) []float64 {
	var out []float64
	for _, r := range rs {
		if v, ok := m.get(r); ok {
			out = append(out, v)
		}
	}
	return out
}

// comparable lists reasons the runs should not be compared: different
// scenarios, load plans or generator setups.
func comparable(rs []*Report) []string {
	var probs []string
	first := rs[0]
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			probs = append(probs, p)
		}
	}
	for _, r := range rs[1:] {
		if r.Scenario != first.Scenario {
			add(fmt.Sprintf("different scenarios (%s and %s)", first.Scenario, r.Scenario))
		}
		if r.Load.Executor != first.Load.Executor || r.Load.Mode != first.Load.Mode || !near(r.Load.Peak, first.Load.Peak) {
			add("different load plans")
		}
		if math.Abs(r.Duration-first.Duration) > 0.1*first.Duration+1 && r.Breakpoint == nil {
			add("different durations")
		}
		if r.Load.Workers != first.Load.Workers {
			add(fmt.Sprintf("different worker counts (%d and %d)", first.Load.Workers, r.Load.Workers))
		}
		if r.Verdict == VerdictGeneratorLimit || first.Verdict == VerdictGeneratorLimit {
			add("a run was generator-limited")
		}
	}
	return probs
}

func mean(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// spread is the largest relative deviation of a repeat from its mean.
func spread(v []float64) float64 {
	m := mean(v)
	if len(v) < 2 || m == 0 {
		return 0
	}
	s := 0.0
	for _, x := range v {
		s = math.Max(s, math.Abs(x-m)/math.Abs(m))
	}
	return s
}

func relChange(a, b float64) float64 {
	if a == 0 {
		if b == 0 {
			return 0
		}
		return math.Inf(1)
	}
	return (b - a) / math.Abs(a)
}

// bootstrapCI resamples the runs of each version with replacement and
// returns the percentile interval of the relative change in means.
func bootstrapCI(rng *rand.Rand, a, b []float64, level float64, n int) (float64, float64) {
	if len(a) < 2 || len(b) < 2 {
		c := relChange(mean(a), mean(b))
		return c, c
	}
	draws := make([]float64, 0, n)
	sa, sb := make([]float64, len(a)), make([]float64, len(b))
	for i := 0; i < n; i++ {
		for j := range sa {
			sa[j] = a[rng.IntN(len(a))]
		}
		for j := range sb {
			sb[j] = b[rng.IntN(len(b))]
		}
		c := relChange(mean(sa), mean(sb))
		if !math.IsInf(c, 0) && !math.IsNaN(c) {
			draws = append(draws, c)
		}
	}
	if len(draws) == 0 {
		return 0, 0
	}
	sort.Float64s(draws)
	lo := draws[int(math.Floor((1-level)/2*float64(len(draws))))]
	hi := draws[int(math.Min(float64(len(draws)-1), math.Ceil((1+level)/2*float64(len(draws)))-1))]
	return lo, hi
}

func judge(d MetricDelta, repeated bool) string {
	if d.MeanA == 0 && d.MeanB == 0 {
		return CmpNoChange
	}
	worse := d.Change > 0
	if d.HigherIsBetter {
		worse = d.Change < 0
	}
	big := math.Abs(d.Change) > d.NoiseFloor
	if isLatency(d.Name) && math.Abs(d.MeanB-d.MeanA) < minLatencyChange {
		big = false
	}
	excludesZero := d.CILow > 0 || d.CIHigh < 0
	switch {
	case !big:
		return CmpNoChange
	case !repeated:
		// One run per side cannot separate a change from noise.
		return CmpInconclusive
	case !excludesZero:
		return CmpInconclusive
	case worse:
		return CmpRegression
	default:
		return CmpImprovement
	}
}

func isLatency(name string) bool { return name == "p50" || name == "p95" || name == "p99" }

func fmtMetric(name string, v float64) string {
	switch name {
	case "error rate":
		return Pct(v)
	case "throughput":
		return fmt.Sprintf("%.1f/s", v)
	case "max sustainable load":
		return num(v)
	default:
		return Ms(v)
	}
}

func signed(f float64) string {
	if math.IsInf(f, 1) {
		return "+∞"
	}
	return fmt.Sprintf("%+.1f%%", f*100)
}

// WriteText writes the comparison as a terminal table.
func (c *Comparison) WriteText(w io.Writer) {
	fmt.Fprintf(w, "\n  %s  %s (%d runs) → %s (%d runs)\n\n", strings.ToUpper(c.Verdict), c.A.Label, len(c.A.Runs), c.B.Label, len(c.B.Runs))
	fmt.Fprintf(w, "  %-22s %12s %12s %9s %21s %8s  %s\n", "metric", c.A.Label, c.B.Label, "change", "95% interval", "noise", "verdict")
	for _, m := range c.Metrics {
		fmt.Fprintf(w, "  %-22s %12s %12s %9s %10s … %-8s %7s  %s\n", m.Name, fmtMetric(m.Name, m.MeanA), fmtMetric(m.Name, m.MeanB),
			signed(m.Change), signed(m.CILow), signed(m.CIHigh), fmt.Sprintf("±%.0f%%", m.NoiseFloor*100), m.Verdict)
	}
	for _, p := range c.Problems {
		fmt.Fprintf(w, "\n  not comparable: %s", p)
	}
	if len(c.A.Runs) < 3 || len(c.B.Runs) < 3 {
		fmt.Fprintf(w, "\n  note: use at least 3 runs per version; with fewer, changes are reported as inconclusive.")
	}
	fmt.Fprintln(w)
}

// WriteMarkdown writes the comparison for a pull request comment.
func (c *Comparison) WriteMarkdown(w io.Writer) {
	icon := map[string]string{CmpRegression: "❌", CmpImprovement: "✅", CmpNoChange: "➖", CmpInconclusive: "⚠️"}[c.Verdict]
	fmt.Fprintf(w, "### %s Stampede comparison: %s\n\n%s (%d runs) → %s (%d runs)\n\n", icon, c.Verdict, c.A.Label, len(c.A.Runs), c.B.Label, len(c.B.Runs))
	fmt.Fprintf(w, "| Metric | %s | %s | Change | 95%% interval | Noise | Verdict |\n|---|---:|---:|---:|---:|---:|---|\n", c.A.Label, c.B.Label)
	for _, m := range c.Metrics {
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s … %s | ±%.0f%% | %s |\n", m.Name, fmtMetric(m.Name, m.MeanA), fmtMetric(m.Name, m.MeanB),
			signed(m.Change), signed(m.CILow), signed(m.CIHigh), m.NoiseFloor*100, m.Verdict)
	}
	for _, p := range c.Problems {
		fmt.Fprintf(w, "\n> Not comparable: %s", p)
	}
	fmt.Fprintln(w)
}
