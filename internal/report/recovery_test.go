package report

import (
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/scenario"
)

func spikePlan() *scenario.Plan {
	st := func(d time.Duration, v float64) scenario.PlanStage { return scenario.PlanStage{Duration: d, Target: v} }
	return &scenario.Plan{Shape: "spike", Mode: scenario.ModeRate, Stages: []scenario.PlanStage{
		st(10*time.Second, 10), st(20*time.Second, 10), st(5*time.Second, 100), st(20*time.Second, 100), st(5*time.Second, 10), st(30*time.Second, 10),
	}}
}

// timeline: 20ms p95 at normal load, 900ms under the spike, then slow for
// `slow` seconds after load is back to normal (at 60s).
func spikeTimeline(slow int, errDuringSlow float64) []Point {
	var tl []Point
	for s := 0; s < 90; s++ {
		p := Point{T: float64(s), RPS: 10, P95: 0.020}
		switch {
		case s >= 30 && s < 60:
			p.P95, p.RPS = 0.900, 100
		case s >= 60 && s < 60+slow:
			p.P95, p.ErrorRate = 0.300, errDuringSlow
		}
		tl = append(tl, p)
	}
	return tl
}

func TestRecovery(t *testing.T) {
	r := findRecovery(spikePlan(), spikeTimeline(7, 0.2))
	if r == nil || !r.Recovered || r.Seconds != 7 || r.NormalAt != 60 || r.BaselineP95 != 0.020 {
		t.Fatalf("%+v", r)
	}
	// Within 25% of the baseline counts as normal at once.
	tl := spikeTimeline(0, 0)
	tl[60].P95 = 0.024
	if r := findRecovery(spikePlan(), tl); r == nil || !r.Recovered || r.Seconds != 0 {
		t.Fatalf("%+v", r)
	}
	// Never back: slow to the end.
	if r := findRecovery(spikePlan(), spikeTimeline(30, 0)); r == nil || r.Recovered {
		t.Fatalf("%+v", r)
	}
	// One good second inside a slow patch is not recovery.
	tl = spikeTimeline(10, 0)
	tl[63].P95 = 0.020
	if r := findRecovery(spikePlan(), tl); r == nil || r.Seconds != 10 {
		t.Fatalf("%+v", r)
	}
	if r := findRecovery(&scenario.Plan{Shape: "stress"}, tl); r != nil {
		t.Fatal("recovery for a stress shape")
	}
}
