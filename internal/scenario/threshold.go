package scenario

import (
	"fmt"
	"strconv"
	"strings"
)

// Metric names that thresholds can refer to.
const (
	MetricP50     = "p50"
	MetricP90     = "p90"
	MetricP95     = "p95"
	MetricP99     = "p99"
	MetricP999    = "p99.9"
	MetricMax     = "max"
	MetricMean    = "mean"
	MetricErrors  = "errors"
	MetricChecks  = "checks"
	MetricDropped = "dropped"
	MetricRPS     = "rps"
	MetricCount   = "count"
)

// metricOrder lists metric suffixes longest first so "p99.9" wins over "p99".
var metricOrder = []string{MetricP999, MetricDropped, MetricErrors, MetricChecks, MetricCount, MetricMean, MetricP50, MetricP90, MetricP95, MetricP99, MetricMax, MetricRPS}

// MetricKind says how a metric's value is expressed.
type MetricKind int

// Metric kinds.
const (
	KindLatency MetricKind = iota
	KindRatio
	KindRate
	KindCount
)

// KindOf returns the unit family of a metric.
func KindOf(metric string) MetricKind {
	switch metric {
	case MetricErrors, MetricChecks, MetricDropped:
		return KindRatio
	case MetricRPS:
		return KindRate
	case MetricCount:
		return KindCount
	default:
		return KindLatency
	}
}

// ScopeAll is the threshold scope that covers every request.
const ScopeAll = "http"

// Threshold is a parsed target such as "checkout.p95 < 800ms".
type Threshold struct {
	Source string
	// Scope is ScopeAll, a journey name, or "journey/step".
	Scope  string
	Metric string
	Op     string
	// Value is seconds for latency, a fraction for ratios, per second for
	// rates and a plain number for counts.
	Value float64
}

// Pass reports whether observed satisfies the threshold.
func (t Threshold) Pass(observed float64) bool {
	switch t.Op {
	case "<":
		return observed < t.Value
	case "<=":
		return observed <= t.Value
	case ">":
		return observed > t.Value
	case ">=":
		return observed >= t.Value
	case "==":
		return observed == t.Value
	}
	return false
}

// FormatValue renders a value in the metric's unit.
func (t Threshold) FormatValue(v float64) string {
	return FormatMetric(t.Metric, v)
}

// FormatMetric renders a value in a metric's natural unit.
func FormatMetric(metric string, v float64) string {
	switch KindOf(metric) {
	case KindRatio:
		return strconv.FormatFloat(v*100, 'f', 2, 64) + "%"
	case KindRate:
		return strconv.FormatFloat(v, 'f', 1, 64) + "/s"
	case KindCount:
		return strconv.FormatFloat(v, 'f', 0, 64)
	default:
		ms := v * 1000
		if ms >= 1000 {
			return strconv.FormatFloat(v, 'f', 2, 64) + "s"
		}
		return strconv.FormatFloat(ms, 'f', 1, 64) + "ms"
	}
}

var thresholdOps = []string{"<=", ">=", "==", "<", ">"}

// ParseThreshold parses "[scope.]metric op value".
func ParseThreshold(src string) (Threshold, error) {
	t := Threshold{Source: strings.TrimSpace(src)}
	opAt := -1
	for _, op := range thresholdOps {
		if i := strings.Index(t.Source, op); i >= 0 && (opAt < 0 || i < opAt) {
			opAt, t.Op = i, op
		}
	}
	if opAt < 0 {
		return t, fmt.Errorf("target %q needs a comparison such as <, <=, >, >= or ==", src)
	}
	lhs := strings.TrimSpace(t.Source[:opAt])
	rhs := strings.TrimSpace(t.Source[opAt+len(t.Op):])

	for _, m := range metricOrder {
		if lhs == m {
			t.Scope, t.Metric = ScopeAll, m
			break
		}
		if strings.HasSuffix(lhs, "."+m) {
			t.Scope, t.Metric = strings.TrimSuffix(lhs, "."+m), m
			break
		}
	}
	if t.Metric == "" {
		return t, fmt.Errorf("target %q: unknown metric (use p50, p90, p95, p99, p99.9, max, mean, errors, checks, dropped, rps or count)", src)
	}
	if t.Scope == "" {
		return t, fmt.Errorf("target %q: empty scope", src)
	}

	switch KindOf(t.Metric) {
	case KindLatency:
		d, err := ParseDuration(rhs)
		if err != nil {
			return t, fmt.Errorf("target %q: %w", src, err)
		}
		t.Value = d.D().Seconds()
	case KindRatio:
		p, err := ParsePercent(rhs)
		if err != nil {
			return t, fmt.Errorf("target %q: %w", src, err)
		}
		t.Value = float64(p)
	case KindRate:
		r, err := ParseRate(rhs)
		if err != nil {
			return t, fmt.Errorf("target %q: %w", src, err)
		}
		t.Value = r.PerSecond()
	case KindCount:
		f, err := strconv.ParseFloat(rhs, 64)
		if err != nil {
			return t, fmt.Errorf("target %q: invalid count %q", src, rhs)
		}
		t.Value = f
	}
	return t, nil
}

// JourneyThresholds turns a journey's target block into thresholds.
func JourneyThresholds(j Journey) []Threshold {
	if j.Target == nil {
		return nil
	}
	var out []Threshold
	add := func(metric string, d Duration) {
		if d > 0 {
			out = append(out, Threshold{
				Source: fmt.Sprintf("%s.%s < %s", j.Name, metric, d),
				Scope:  j.Name, Metric: metric, Op: "<", Value: d.D().Seconds(),
			})
		}
	}
	add(MetricP50, j.Target.P50)
	add(MetricP90, j.Target.P90)
	add(MetricP95, j.Target.P95)
	add(MetricP99, j.Target.P99)
	if j.Target.Errors != nil {
		out = append(out, Threshold{
			Source: fmt.Sprintf("%s.errors < %s", j.Name, j.Target.Errors),
			Scope:  j.Name, Metric: MetricErrors, Op: "<", Value: float64(*j.Target.Errors),
		})
	}
	return out
}
