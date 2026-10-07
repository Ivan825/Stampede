package report

import (
	"math"
	"sort"
	"time"

	"github.com/Ivan825/Stampede/internal/scenario"
)

// Recovery is how long the target took to return to normal after an
// overload: set for the spike and recovery shapes, which hold normal
// load, push past it, then return to it.
type Recovery struct {
	// Recovered is false when the run ended before the target was back.
	Recovered bool `json:"recovered"`
	// Seconds from load returning to normal to the target holding its
	// baseline again.
	Seconds float64 `json:"seconds,omitempty"`
	// NormalAt is when load returned to normal, in seconds since start.
	NormalAt float64 `json:"normalAt"`
	// Baseline is the target before the overload: median p95 (seconds)
	// and error rate over the normal-load hold.
	BaselineP95       float64 `json:"baselineP95"`
	BaselineErrorRate float64 `json:"baselineErrorRate"`
}

// Recovery thresholds: back to normal means p95 within 25% (and 5ms) of
// the baseline and the error rate within one point of it, for
// recoverySeconds seconds in a row.
const (
	recoveryP95Factor = 1.25
	recoveryP95Slack  = 0.005
	recoveryErrSlack  = 0.01
	recoverySeconds   = 3
)

// findRecovery reads a spike or recovery plan's stages: ramp to normal,
// hold normal (the baseline), ramp up, hold the peak, ramp down, hold
// normal (where recovery is measured).
func findRecovery(plan *scenario.Plan, tl []Point) *Recovery {
	if plan.Shape != "spike" && plan.Shape != "recovery" || len(plan.Stages) != 6 {
		return nil
	}
	var bounds [7]time.Duration
	for i, s := range plan.Stages {
		bounds[i+1] = bounds[i] + s.Duration
	}
	at := func(p Point) time.Duration { return time.Duration(p.T * float64(time.Second)) }

	var p95s []float64
	var reqs, errs float64
	for _, p := range tl {
		// The middle of the baseline hold, past any warm-up.
		if t := at(p); t >= bounds[1]+(bounds[2]-bounds[1])/4 && t < bounds[2] && p.RPS > 0 {
			p95s = append(p95s, p.P95)
			reqs += p.RPS
			errs += p.RPS * p.ErrorRate
		}
	}
	if len(p95s) == 0 {
		return nil
	}
	sort.Float64s(p95s)
	rec := &Recovery{NormalAt: bounds[5].Seconds(), BaselineP95: p95s[len(p95s)/2]}
	if reqs > 0 {
		rec.BaselineErrorRate = errs / reqs
	}
	limitP95 := math.Max(rec.BaselineP95*recoveryP95Factor, rec.BaselineP95+recoveryP95Slack)
	streak := 0
	for _, p := range tl {
		if at(p) < bounds[5] {
			continue
		}
		if p.RPS > 0 && p.P95 <= limitP95 && p.ErrorRate <= rec.BaselineErrorRate+recoveryErrSlack {
			streak++
		} else {
			streak = 0
		}
		if streak == recoverySeconds {
			rec.Recovered = true
			// The first second of the streak.
			rec.Seconds = math.Max(0, p.T-float64(recoverySeconds-1)-rec.NormalAt)
			break
		}
	}
	return rec
}
