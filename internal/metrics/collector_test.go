package metrics

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestCollectorConcurrent(t *testing.T) {
	c := NewCollector(8)
	t0 := time.Now()
	var wg sync.WaitGroup
	for vu := 0; vu < 64; vu++ {
		wg.Add(1)
		go func(vu int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				c.IterationStarted(vu, 0, time.Millisecond)
				c.Record(vu, &Sample{
					Step: i % 3, Intended: t0, Start: t0.Add(time.Millisecond), End: t0.Add(11 * time.Millisecond),
					Status: 200, BytesIn: 100, Phases: [NumPhases]time.Duration{PhaseWait: 9 * time.Millisecond},
				})
				c.IterationDone(vu, 0, 10*time.Millisecond, true)
			}
		}(vu)
	}
	wg.Wait()
	c.Dropped(5)
	s := c.Flush(0, 64, 64)
	tot := s.Totals()
	if tot.Requests != 64000 || tot.BytesIn != 6400000 || tot.Status[200] != 64000 {
		t.Fatalf("totals wrong: %+v", tot)
	}
	if got := tot.Latency.Quantile(0.5); got < 10990 || got > 11010 {
		t.Errorf("latency from intended should be ~11ms, got %dµs", got)
	}
	if got := tot.Service.Quantile(0.5); got < 9990 || got > 10010 {
		t.Errorf("service time should be ~10ms, got %dµs", got)
	}
	if s.Journeys[0].Completed != 64000 || s.Dropped != 5 || s.Seq != 1 {
		t.Errorf("journey/dropped/seq wrong: %+v %d %d", s.Journeys[0], s.Dropped, s.Seq)
	}
	if next := c.Flush(1, 0, 0); !next.Empty() || next.Seq != 2 {
		t.Errorf("second flush should be empty with seq 2")
	}
	ph := c.PhaseHistograms()
	if ph[0][PhaseWait].Count() == 0 {
		t.Error("phase histogram missing")
	}
}

func TestSnapshotJSONRoundTrip(t *testing.T) {
	s := NewSnapshot(3)
	now := time.Now()
	s.Step(1).Add(&Sample{Start: now, End: now.Add(5 * time.Millisecond), Status: 503, Err: "HTTP 503", Failed: true})
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var d Snapshot
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	st := d.Steps[1]
	if st.Failed != 1 || st.Errors["HTTP 503"] != 1 || st.Latency.Count() != 1 {
		t.Fatalf("round trip lost data: %s", b)
	}
}

func TestStreamStats(t *testing.T) {
	now := time.Now()
	samples := []Sample{
		// A stream of 11 events whose last 10 arrived over 2s: 5 events/s.
		{Start: now, End: now.Add(3 * time.Second), Events: 11, StreamTime: 2 * time.Second, Proto: "HTTP/1.1"},
		// A stream that ended before any event counts no stream.
		{Start: now, End: now.Add(time.Second), Proto: "HTTP/1.1"},
		{Start: now, End: now.Add(time.Second), Events: 1, Proto: "HTTP/2.0"},
	}
	a, b := NewSnapshot(0), NewSnapshot(0)
	for i := range samples {
		dst := a
		if i == 2 {
			dst = b
		}
		dst.Step(0).Add(&samples[i])
	}
	a.Merge(b)
	st := a.Steps[0]
	tests := []struct {
		name      string
		got, want uint64
	}{
		{"streams", st.Streams, 2},
		{"events", st.Events, 12},
		{"stream µs", st.StreamUs, 2_000_000},
		{"HTTP/1.1", st.Protocols["HTTP/1.1"], 2},
		{"HTTP/2.0", st.Protocols["HTTP/2.0"], 1},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}
