package report

import (
	"math"
	"strings"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// Evaluate checks each threshold against merged metrics covering dur
// seconds.
func Evaluate(p *scenario.Program, ths []scenario.Threshold, total *metrics.Snapshot, dur float64) []Check {
	out := make([]Check, 0, len(ths))
	for _, t := range ths {
		obs := Observe(p, t.Scope, t.Metric, total, dur)
		out = append(out, Check{
			Source: t.Source, Scope: t.Scope, Metric: t.Metric, Op: t.Op,
			Target: t.Value, Observed: obs, Pass: !math.IsNaN(obs) && t.Pass(obs),
			TargetText: t.FormatValue(t.Value), ObservedText: formatObserved(t, obs),
		})
	}
	return out
}

func formatObserved(t scenario.Threshold, v float64) string {
	if math.IsNaN(v) {
		return "no data"
	}
	return t.FormatValue(v)
}

// Observe computes one metric for a scope ("http", a journey, or
// "journey/step"). It returns NaN when the scope has no data.
func Observe(p *scenario.Program, scope, metric string, total *metrics.Snapshot, dur float64) float64 {
	st := scopeStats(p, scope, total)
	if metric == scenario.MetricDropped {
		var started uint64
		for _, j := range total.Journeys {
			started += j.Started
		}
		if started+total.Dropped == 0 {
			return math.NaN()
		}
		return float64(total.Dropped) / float64(started+total.Dropped)
	}
	if st.Requests == 0 {
		if metric == scenario.MetricCount || metric == scenario.MetricRPS {
			return 0
		}
		return math.NaN()
	}
	us := func(v uint64) float64 { return float64(v) / 1e6 }
	switch metric {
	case scenario.MetricP50:
		return us(st.Latency.Quantile(0.5))
	case scenario.MetricP90:
		return us(st.Latency.Quantile(0.9))
	case scenario.MetricP95:
		return us(st.Latency.Quantile(0.95))
	case scenario.MetricP99:
		return us(st.Latency.Quantile(0.99))
	case scenario.MetricP999:
		return us(st.Latency.Quantile(0.999))
	case scenario.MetricMax:
		return us(st.Latency.Max())
	case scenario.MetricMean:
		return st.Latency.Mean() / 1e6
	case scenario.MetricErrors:
		return float64(st.Failed) / float64(st.Requests)
	case scenario.MetricChecks:
		n := st.ChecksPassed + st.ChecksFailed
		if n == 0 {
			return math.NaN()
		}
		return float64(st.ChecksPassed) / float64(n)
	case scenario.MetricRPS:
		if dur <= 0 {
			return math.NaN()
		}
		return float64(st.Requests) / dur
	case scenario.MetricCount:
		return float64(st.Requests)
	}
	return math.NaN()
}

func scopeStats(p *scenario.Program, scope string, total *metrics.Snapshot) *metrics.StepStats {
	if scope == scenario.ScopeAll {
		return total.Totals()
	}
	out := &metrics.StepStats{Latency: metrics.NewHistogram(), Service: metrics.NewHistogram()}
	journey, step, hasStep := strings.Cut(scope, "/")
	for _, cs := range p.Steps {
		if cs.Journey != journey || (hasStep && cs.Name != step) {
			continue
		}
		if st := total.Steps[cs.ID]; st != nil {
			out.Merge(st)
		}
	}
	return out
}

// AllPass reports whether every check passed.
func AllPass(cs []Check) bool {
	for _, c := range cs {
		if !c.Pass {
			return false
		}
	}
	return true
}
