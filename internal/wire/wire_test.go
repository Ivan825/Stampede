package wire

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	workerv1 "github.com/Ivan825/Stampede/gen/stampede/worker/v1"
	"github.com/Ivan825/Stampede/internal/metrics"
)

func sampleSnapshot() *metrics.Snapshot {
	s := metrics.NewSnapshot(7)
	s.Seq = 8
	s.Dropped = 3
	s.VUs = 12
	s.Planned = 99.5
	t0 := time.Unix(1000, 0)
	for i := 0; i < 50; i++ {
		smp := &metrics.Sample{
			Step: i % 3, Start: t0, End: t0.Add(time.Duration(i+1) * time.Millisecond),
			Intended: t0.Add(-time.Millisecond), Status: 200 + (i%2)*300,
			Phases:  [metrics.NumPhases]time.Duration{time.Millisecond, 2 * time.Millisecond, 0, 3 * time.Millisecond, time.Microsecond},
			BytesIn: 100, BytesOut: 10, ChecksPassed: 1,
		}
		if i%5 == 0 {
			smp.Failed, smp.Err, smp.ChecksFailed = true, "timeout", 1
		}
		s.Step(smp.Step).Add(smp)
	}
	j := s.Journey(1)
	j.Started, j.Completed, j.Failed = 10, 8, 1
	j.Duration.RecordDuration(40 * time.Millisecond)
	s.SchedLag.RecordDuration(250 * time.Microsecond)
	return s
}

// canonical renders a snapshot as JSON so histogram contents (which marshal
// as their binary encoding) and every counter are compared.
func canonical(t *testing.T, s *metrics.Snapshot) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSnapshotRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		snap   *metrics.Snapshot
		health *Health
	}{
		{"empty", metrics.NewSnapshot(0), nil},
		{"full", sampleSnapshot(), &Health{
			Saturated: true, Reasons: []string{"cpu 91% > 85% for 5s"}, CPUPercent: 91.5,
			SchedLagP99: 12 * time.Millisecond, GCPauseP99: time.Millisecond,
			OpenFDs: 900, FDLimit: 1024, Goroutines: 4000, Dropped: 3,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := SnapshotToProto("run-1", tt.snap, tt.health)
			if err != nil {
				t.Fatal(err)
			}
			// Through the real wire encoding, not just the Go structs.
			b, err := proto.Marshal(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_Snapshot{Snapshot: p}})
			if err != nil {
				t.Fatal(err)
			}
			var m workerv1.WorkerMessage
			if err := proto.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			got, h, err := SnapshotFromProto("w1", m.GetSnapshot())
			if err != nil {
				t.Fatal(err)
			}
			if m.GetSnapshot().GetRunId() != "run-1" || got.Worker != "w1" {
				t.Errorf("run %q worker %q", m.GetSnapshot().GetRunId(), got.Worker)
			}
			want := *tt.snap
			want.Worker = "w1"
			if a, b := canonical(t, got), canonical(t, &want); a != b {
				t.Errorf("round trip changed the snapshot:\n got %s\nwant %s", a, b)
			}
			if !reflect.DeepEqual(h, tt.health) {
				t.Errorf("health %+v, want %+v", h, tt.health)
			}
			// Totals and quantiles survive too.
			if len(tt.snap.Steps) > 0 {
				if a, b := got.Totals().Latency.Quantile(0.99), tt.snap.Totals().Latency.Quantile(0.99); a != b {
					t.Errorf("p99 %d, want %d", a, b)
				}
			}
		})
	}
}

func TestSnapshotFromProtoRejectsCorruptHistogram(t *testing.T) {
	p, err := SnapshotToProto("r", sampleSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Steps[0].Latency = []byte{9, 9, 9}
	if _, _, err := SnapshotFromProto("w", p); err == nil || !strings.Contains(err.Error(), "latency") {
		t.Errorf("got %v, want a latency decode error", err)
	}
}

func TestPhasesRoundTrip(t *testing.T) {
	c := metrics.NewCollector(2)
	t0 := time.Unix(0, 0)
	for i := 0; i < 20; i++ {
		c.Record(i, &metrics.Sample{Step: i % 2, Start: t0, End: t0.Add(time.Millisecond),
			Phases: [metrics.NumPhases]time.Duration{time.Duration(i) * time.Millisecond, time.Millisecond, 0, 5 * time.Millisecond, 0}})
	}
	in := c.PhaseHistograms()
	p, err := PhasesToProto(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := PhasesFromProto(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("%d steps, want %d", len(out), len(in))
	}
	for id, hs := range in {
		for i, h := range hs {
			g := out[id][i]
			if g.Count() != h.Count() || g.Sum() != h.Sum() || g.Quantile(0.95) != h.Quantile(0.95) {
				t.Errorf("step %d phase %d: got count=%d sum=%d, want %d %d", id, i, g.Count(), g.Sum(), h.Count(), h.Sum())
			}
		}
	}
	merged := map[int]*[metrics.NumPhases]*metrics.Histogram{}
	MergePhases(merged, out)
	MergePhases(merged, out)
	if got, want := merged[0][metrics.PhaseWait].Count(), 2*in[0][metrics.PhaseWait].Count(); got != want {
		t.Errorf("merged count %d, want %d", got, want)
	}
}

func TestCheckVersion(t *testing.T) {
	tests := []struct {
		name string
		v    *workerv1.ProtocolVersion
		ok   bool
	}{
		{"same", Version(), true},
		{"newer minor", &workerv1.ProtocolVersion{Major: ProtocolMajor, Minor: ProtocolMinor + 3}, true},
		{"other major", &workerv1.ProtocolVersion{Major: ProtocolMajor + 1}, false},
		{"missing", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckVersion(tt.v)
			if (err == nil) != tt.ok {
				t.Fatalf("CheckVersion = %v, ok want %v", err, tt.ok)
			}
			if err != nil && !strings.Contains(err.Error(), "protocol") {
				t.Errorf("unclear error %q", err)
			}
		})
	}
}
