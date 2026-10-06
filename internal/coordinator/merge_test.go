package coordinator

import (
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
)

func snap(interval int64, seq uint64, requests uint64) *metrics.Snapshot {
	s := metrics.NewSnapshot(interval)
	s.Seq = seq
	s.VUs = 1
	st := s.Step(0)
	st.Requests = requests
	return s
}

func requests(s *metrics.Snapshot) uint64 {
	if st := s.Steps[0]; st != nil {
		return st.Requests
	}
	return 0
}

func allExpected(int, int64) bool { return true }

func TestMergerDedupesResends(t *testing.T) {
	t0 := time.Now()
	m := newMerger(2, t0, time.Second, 2*time.Second, func(int64) float64 { return 10 })
	if dup, _ := m.add(0, snap(0, 1, 5)); dup {
		t.Fatal("first snapshot reported as duplicate")
	}
	// The same (worker, seq) again, as after a reconnect.
	if dup, _ := m.add(0, snap(0, 1, 5)); !dup {
		t.Fatal("resend not recognised")
	}
	// Same seq from another worker is not a duplicate.
	if dup, _ := m.add(1, snap(0, 1, 7)); dup {
		t.Fatal("other worker's snapshot treated as duplicate")
	}
	out := m.ready(t0, allExpected, false)
	if len(out) != 1 || requests(out[0].snap) != 12 || len(out[0].missing) != 0 {
		t.Fatalf("got %+v", out)
	}
	if out[0].snap.VUs != 2 || out[0].snap.Planned != 10 || out[0].snap.Seq != 1 {
		t.Errorf("gauges vus=%d planned=%v seq=%d", out[0].snap.VUs, out[0].snap.Planned, out[0].snap.Seq)
	}
	// A resend after emission is still ignored.
	if dup, _ := m.add(1, snap(0, 1, 7)); !dup {
		t.Error("resend after emission not recognised")
	}
	if got := requests(m.all()[0]); got != 12 {
		t.Errorf("final interval has %d requests, want 12", got)
	}
}

func TestMergerWaitsForAllThenTimesOut(t *testing.T) {
	t0 := time.Now()
	m := newMerger(3, t0, time.Second, 2*time.Second, func(int64) float64 { return 0 })
	m.add(0, snap(0, 1, 1))
	m.add(1, snap(0, 1, 1))
	// Worker 2 is behind: nothing emitted before the grace period ends.
	if out := m.ready(t0.Add(2*time.Second), allExpected, false); len(out) != 0 {
		t.Fatalf("emitted early: %+v", out)
	}
	// Interval 0 ends at 1s; with 2s grace it goes out after 3s, marking
	// worker 2 missing.
	out := m.ready(t0.Add(3*time.Second+time.Millisecond), allExpected, false)
	if len(out) != 1 || len(out[0].missing) != 1 || out[0].missing[0] != 2 {
		t.Fatalf("got %+v", out)
	}
	// Its late data still reaches the final result, not the live copy.
	if _, late := m.add(2, snap(0, 1, 4)); !late {
		t.Error("late data not flagged")
	}
	if requests(out[0].snap) != 2 {
		t.Errorf("live copy changed to %d", requests(out[0].snap))
	}
	if got := requests(m.all()[0]); got != 6 {
		t.Errorf("final interval has %d requests, want 6", got)
	}
}

func TestMergerEmitsInOrder(t *testing.T) {
	t0 := time.Now()
	m := newMerger(2, t0, time.Second, time.Hour, func(int64) float64 { return 0 })
	// Interval 1 complete before interval 0: nothing goes out of order.
	m.add(0, snap(1, 2, 1))
	m.add(1, snap(1, 2, 1))
	m.add(0, snap(0, 1, 1))
	if out := m.ready(t0, allExpected, false); len(out) != 0 {
		t.Fatalf("emitted %d intervals before interval 0 was complete", len(out))
	}
	m.add(1, snap(0, 1, 1))
	out := m.ready(t0, allExpected, false)
	if len(out) != 2 || out[0].snap.Interval != 0 || out[1].snap.Interval != 1 {
		t.Fatalf("got %+v", out)
	}
}

func TestMergerSkipsWorkersNotExpected(t *testing.T) {
	t0 := time.Now()
	m := newMerger(2, t0, time.Second, time.Hour, func(int64) float64 { return 0 })
	m.add(0, snap(0, 1, 1))
	// Worker 1 was lost: not expected, so interval 0 is complete.
	lost := func(i int, _ int64) bool { return i != 1 }
	out := m.ready(t0, lost, false)
	if len(out) != 1 || len(out[0].missing) != 0 {
		t.Fatalf("got %+v", out)
	}
}

func TestMergerForceFlushes(t *testing.T) {
	t0 := time.Now()
	m := newMerger(2, t0, time.Second, time.Hour, func(int64) float64 { return 0 })
	m.add(0, snap(0, 1, 1))
	m.add(0, snap(1, 2, 1))
	out := m.ready(t0, allExpected, true)
	if len(out) != 2 {
		t.Fatalf("force emitted %d intervals, want 2", len(out))
	}
	for _, e := range out {
		if len(e.missing) != 1 || e.missing[0] != 1 {
			t.Errorf("interval %d missing %v", e.snap.Interval, e.missing)
		}
	}
	if len(m.all()) != 2 {
		t.Errorf("stored %d", len(m.all()))
	}
}

func TestWindows(t *testing.T) {
	got := windows([]int64{7, 1, 2, 3, 5, 6, 10})
	want := []Window{{1, 3}, {5, 7}, {10, 10}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("window %d = %v, want %v", i, got[i], want[i])
		}
	}
	if windows(nil) != nil {
		t.Error("empty input")
	}
}
