package coordinator

import (
	"testing"
	"time"
)

// exchange simulates one ping/pong against a worker whose clock reads
// offset ahead of the server's, with the given one-way delays and worker
// processing time.
func exchange(t1 time.Duration, offset, up, proc, down time.Duration) clockSample {
	t2 := t1 + up + offset
	t3 := t2 + proc
	t4 := t1 + up + proc + down
	return clockSample{t1: int64(t1), t2: int64(t2), t3: int64(t3), t4: int64(t4)}
}

func TestClockSampleSymmetric(t *testing.T) {
	for _, off := range []time.Duration{0, 7 * time.Second, -4 * time.Second, 123456789} {
		s := exchange(time.Hour, off, 3*time.Millisecond, 200*time.Microsecond, 3*time.Millisecond)
		if s.offset() != off {
			t.Errorf("offset %v, want %v", s.offset(), off)
		}
		if s.rtt() != 6*time.Millisecond {
			t.Errorf("rtt %v, want 6ms", s.rtt())
		}
	}
}

func TestEstimateOffsetPicksLowestRTT(t *testing.T) {
	const off = 2500 * time.Millisecond
	samples := []clockSample{
		// Queueing on the way up makes these asymmetric and wrong by
		// half the asymmetry.
		exchange(0, off, 40*time.Millisecond, 0, 2*time.Millisecond),
		exchange(time.Second, off, 2*time.Millisecond, time.Millisecond, 30*time.Millisecond),
		exchange(2*time.Second, off, 2*time.Millisecond, 0, 2100*time.Microsecond), // fastest
		exchange(3*time.Second, off, 9*time.Millisecond, 0, 2*time.Millisecond),
	}
	got, rtt, ok := estimateOffset(samples)
	if !ok {
		t.Fatal("no estimate")
	}
	// The best sample's error is half its asymmetry: 50µs.
	if d := got - off; d < -50*time.Microsecond || d > 50*time.Microsecond {
		t.Errorf("offset %v, want %v ± 50µs", got, off)
	}
	if rtt != 4100*time.Microsecond {
		t.Errorf("rtt %v", rtt)
	}
}

func TestEstimateOffsetSkipsImpossibleSamples(t *testing.T) {
	bad := clockSample{t1: 100, t2: 50, t3: 10_000, t4: 120} // negative rtt
	if _, _, ok := estimateOffset([]clockSample{bad}); ok {
		t.Error("estimate from a sample with negative round trip")
	}
	if _, _, ok := estimateOffset(nil); ok {
		t.Error("estimate from no samples")
	}
}
