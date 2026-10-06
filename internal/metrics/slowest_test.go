package metrics

import (
	"encoding/json"
	"testing"
	"time"
)

func sampleWith(t0 time.Time, ms int, id byte) *Sample {
	s := &Sample{Start: t0, End: t0.Add(time.Duration(ms) * time.Millisecond), Status: 200}
	s.TraceID[15] = id
	return s
}

func latencies(rs []SlowRequest) []int {
	out := make([]int, len(rs))
	for i, r := range rs {
		out[i] = int(r.Latency / time.Millisecond)
	}
	return out
}

func TestSlowestKeepsTopFive(t *testing.T) {
	t0 := time.Now()
	st := NewSnapshot(0).Step(1)
	for i, ms := range []int{5, 40, 7, 90, 12, 3, 60, 41, 8} {
		st.Add(sampleWith(t0, ms, byte(i+1)))
	}
	got := latencies(st.Slowest)
	want := []int{90, 60, 41, 40, 12}
	if len(got) != len(want) {
		t.Fatalf("slowest = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slowest = %v, want %v", got, want)
		}
	}
	if st.Slowest[0].TraceID != "00000000000000000000000000000004" {
		t.Errorf("trace id of the slowest = %q", st.Slowest[0].TraceID)
	}
}

func TestSlowestMergeAndNoTrace(t *testing.T) {
	t0 := time.Now()
	a, b := NewSnapshot(0), NewSnapshot(0)
	for _, ms := range []int{10, 20, 30} {
		a.Step(1).Add(sampleWith(t0, ms, 1))
	}
	for _, ms := range []int{15, 25, 35, 45} {
		s := sampleWith(t0, ms, 0)
		s.TraceID = [16]byte{}
		b.Step(1).Add(s)
	}
	a.Merge(b)
	got := latencies(a.Steps[1].Slowest)
	want := []int{45, 35, 30, 25, 20}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("merged slowest = %v, want %v", got, want)
		}
	}
	if a.Steps[1].Slowest[0].TraceID != "" || a.Steps[1].Slowest[2].TraceID == "" {
		t.Errorf("trace ids: %+v", a.Steps[1].Slowest)
	}
	// Merging must not alias the source's slice.
	b.Steps[1].Slowest[0].Latency = 0
	if a.Steps[1].Slowest[0].Latency == 0 {
		t.Error("merge aliased the source slice")
	}

	blob, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var back Snapshot
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Steps[1].Slowest) != 5 || back.Steps[1].Slowest[0].Latency != 45*time.Millisecond {
		t.Errorf("JSON round trip lost slowest: %+v", back.Steps[1].Slowest)
	}
}

func TestSlowestFromIntendedTime(t *testing.T) {
	t0 := time.Now()
	st := NewSnapshot(0).Step(1)
	st.Add(&Sample{Intended: t0, Start: t0.Add(100 * time.Millisecond), End: t0.Add(110 * time.Millisecond)})
	if st.Slowest[0].Latency != 110*time.Millisecond {
		t.Errorf("slow latency %v should count from the intended time", st.Slowest[0].Latency)
	}
}
