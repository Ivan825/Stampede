package report

import (
	"math"
	"sort"

	"github.com/Ivan825/Stampede/internal/metrics"
)

// CurvePoint is one load level of a run whose load changed over time.
type CurvePoint struct {
	// Offered is the planned load: users, or iterations per second.
	Offered float64 `json:"offered"`
	// Throughput is completed iterations per second.
	Throughput float64 `json:"throughput"`
	RPS        float64 `json:"rps"`
	P50        float64 `json:"p50"`
	P95        float64 `json:"p95"`
	P99        float64 `json:"p99"`
	ErrorRate  float64 `json:"errorRate"`
	Seconds    int     `json:"seconds"`
}

// Knee marks where adding load stops adding throughput.
type Knee struct {
	Found bool `json:"found"`
	// At is the last level that still scaled; Next is the first that did not.
	At     CurvePoint  `json:"at"`
	Next   *CurvePoint `json:"next,omitempty"`
	Unit   string      `json:"unit"`
	Reason string      `json:"reason,omitempty"`
}

// Knee detection thresholds.
const (
	// kneeEfficiency is the share of the ideal throughput gain a level
	// must deliver to count as still scaling.
	kneeEfficiency = 0.5
	// kneeLatency flags a level whose p95 is this many times the p95 of
	// the lightest level.
	kneeLatency = 3.0
	// minLevelSeconds ignores levels too short to judge.
	minLevelSeconds = 3
)

// buildCurve groups intervals by planned load and merges each level's
// histograms, so its percentiles are exact rather than averages of
// per-second percentiles. Intervals within 2% of each other count as one
// level; ramp intervals, whose plan differs from both neighbours, are left
// out because their load is not settled.
func buildCurve(snaps []*metrics.Snapshot, secs, until float64) []CurvePoint {
	type level struct {
		offered float64
		total   *metrics.Snapshot
		n       int
	}
	var levels []*level
	for i, s := range snaps {
		// Partial intervals after the planned end would understate throughput.
		if s.Planned <= 0 || (until > 0 && float64(s.Interval+1)*secs > until+1e-9) {
			continue
		}
		if i > 0 && i < len(snaps)-1 && !near(snaps[i-1].Planned, s.Planned) && !near(snaps[i+1].Planned, s.Planned) {
			continue
		}
		var lv *level
		for _, l := range levels {
			if near(l.offered, s.Planned) {
				lv = l
				break
			}
		}
		if lv == nil {
			lv = &level{offered: s.Planned, total: metrics.NewSnapshot(0)}
			levels = append(levels, lv)
		}
		lv.total.Merge(s)
		lv.n++
	}
	out := make([]CurvePoint, 0, len(levels))
	for _, l := range levels {
		if l.n < minLevelSeconds {
			continue
		}
		t := l.total.Totals()
		if t.Requests == 0 {
			continue
		}
		dur := float64(l.n) * secs
		var iters uint64
		for _, j := range l.total.Journeys {
			iters += j.Completed + j.Failed
		}
		q := t.Latency.Quantiles(0.5, 0.95, 0.99)
		out = append(out, CurvePoint{
			Offered: l.offered, Throughput: float64(iters) / dur, RPS: float64(t.Requests) / dur,
			P50: float64(q[0]) / 1e6, P95: float64(q[1]) / 1e6, P99: float64(q[2]) / 1e6,
			ErrorRate: float64(t.Failed) / float64(t.Requests), Seconds: l.n,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Offered < out[j].Offered })
	return out
}

func near(a, b float64) bool {
	if a == b {
		return true
	}
	return math.Abs(a-b) <= 0.02*math.Max(math.Abs(a), math.Abs(b))
}

// findKnee walks the curve and stops at the first level that no longer
// scales: its throughput gain is under half of what the extra load should
// have added, or its p95 has tripled compared with the lightest level.
func findKnee(c []CurvePoint, mode string) *Knee {
	if len(c) < 3 {
		return nil
	}
	k := &Knee{Unit: Unit(mode)}
	base := c[0]
	// Throughput per unit of offered load at the lightest level. In rate
	// mode it is close to 1; in vus mode it is iterations/s per user.
	perUnit := base.Throughput / base.Offered
	for i := 1; i < len(c); i++ {
		prev, cur := c[i-1], c[i]
		ideal := (cur.Offered - prev.Offered) * perUnit
		gain := cur.Throughput - prev.Throughput
		switch {
		case ideal > 0 && gain < kneeEfficiency*ideal:
			k.Reason = "throughput stopped growing with load"
		case base.P95 > 0 && cur.P95 > kneeLatency*base.P95:
			k.Reason = "p95 latency rose sharply"
		case cur.ErrorRate > 0.05 && cur.ErrorRate > 5*prev.ErrorRate:
			k.Reason = "errors rose sharply"
		default:
			continue
		}
		k.Found, k.At = true, prev
		next := cur
		k.Next = &next
		return k
	}
	k.At = c[len(c)-1]
	return k
}
