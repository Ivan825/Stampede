package report

import (
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
)

// synth builds per-second snapshots for a stepped run: each level holds
// for 5 s and the system saturates at capacity iterations per second.
func synth(levels []float64, capacity float64, baseLatency time.Duration) []*metrics.Snapshot {
	var out []*metrics.Snapshot
	t0 := time.Unix(0, 0)
	i := int64(0)
	for _, lv := range levels {
		for s := 0; s < 5; s++ {
			sn := metrics.NewSnapshot(i)
			sn.Planned = lv
			done := lv
			lat := baseLatency
			if lv > capacity {
				done = capacity
				lat = baseLatency * time.Duration(1+10*(lv-capacity)/capacity)
			}
			for n := 0; n < int(done); n++ {
				sn.Step(0).Add(&metrics.Sample{Start: t0, End: t0.Add(lat), Status: 200})
			}
			sn.Journey(0).Completed = uint64(done)
			out = append(out, sn)
			i++
		}
	}
	return out
}

func TestKneeFound(t *testing.T) {
	snaps := synth([]float64{100, 200, 300, 400, 500}, 320, 10*time.Millisecond)
	c := buildCurve(snaps, 1, 25)
	if len(c) != 5 {
		t.Fatalf("levels = %d", len(c))
	}
	if c[1].Throughput != 200 || c[4].Throughput != 320 {
		t.Errorf("throughput %v %v", c[1].Throughput, c[4].Throughput)
	}
	k := findKnee(c, "rate")
	if k == nil || !k.Found || k.At.Offered != 300 || k.Next.Offered != 400 {
		t.Fatalf("knee %+v", k)
	}
}

func TestKneeNotFoundWhenScaling(t *testing.T) {
	c := buildCurve(synth([]float64{100, 200, 300}, 1000, 5*time.Millisecond), 1, 15)
	k := findKnee(c, "rate")
	if k == nil || k.Found || k.At.Offered != 300 {
		t.Fatalf("knee %+v", k)
	}
}

func TestCurveSkipsRamps(t *testing.T) {
	snaps := synth([]float64{100, 200}, 1000, time.Millisecond)
	// Turn the first second of the second level into a ramp value.
	snaps[5].Planned = 150
	c := buildCurve(snaps, 1, 10)
	if len(c) != 2 || c[1].Seconds != 4 {
		t.Fatalf("curve %+v", c)
	}
}
