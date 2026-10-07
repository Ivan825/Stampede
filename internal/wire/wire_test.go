package wire

import (
	"encoding/json"
	"fmt"
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
			Phases:  [metrics.NumPhases]time.Duration{time.Millisecond, 2 * time.Millisecond, 0, 3 * time.Millisecond, time.Microsecond, 4 * time.Millisecond},
			BytesIn: 100, BytesOut: 10, ChecksPassed: 1, Proto: "HTTP/2.0",
			Events: i % 4, StreamTime: time.Duration(i) * time.Millisecond,
		}
		if i%5 == 0 {
			smp.Failed, smp.Err, smp.ChecksFailed = true, "timeout", 1
		}
		if i%2 == 0 {
			smp.TraceID[0], smp.TraceID[15] = byte(i+1), 0xab
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

func TestSnapshotCarriesSlowest(t *testing.T) {
	s := sampleSnapshot()
	p, err := SnapshotToProto("run-1", s, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := SnapshotFromProto("w", p)
	if err != nil {
		t.Fatal(err)
	}
	for id, st := range s.Steps {
		if len(st.Slowest) != metrics.MaxSlowest || len(got.Steps[id].Slowest) != len(st.Slowest) {
			t.Fatalf("step %d: %d slow requests sent, %d received", id, len(st.Slowest), len(got.Steps[id].Slowest))
		}
		for i, r := range st.Slowest {
			g := got.Steps[id].Slowest[i]
			if g.TraceID != r.TraceID || g.Latency != r.Latency || !g.Start.Equal(r.Start) || g.Status != r.Status || g.Err != r.Err {
				t.Errorf("step %d slow %d: sent %+v, received %+v", id, i, r, g)
			}
		}
	}
	p.Steps[0].Slowest[0].TraceId = []byte{1, 2, 3}
	if _, _, err := SnapshotFromProto("w", p); err == nil || !strings.Contains(err.Error(), "trace id") {
		t.Errorf("a malformed trace id should be rejected, got %v", err)
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

func TestSnapshotCarriesErrorExamples(t *testing.T) {
	s := metrics.NewSnapshot(4)
	at := time.Unix(1_700_000_000, 5)
	for i := range 5 {
		s.Step(0).Add(&metrics.Sample{Start: at, End: at.Add(time.Millisecond), Status: 500, Err: "HTTP 500", Failed: true,
			Exchange: &metrics.ErrorExample{At: at, Request: fmt.Sprintf("GET /x?i=%d", i), Status: 500,
				RequestHeaders: map[string]string{"Authorization": "[redacted]"}, ResponseBody: "boom", TraceID: "0102030405060708090a0b0c0d0e0f10"}})
	}
	p, err := SnapshotToProto("run", s, nil)
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := SnapshotFromProto("w", p)
	if err != nil {
		t.Fatal(err)
	}
	exs := back.Steps[0].Examples["HTTP 500"]
	if len(exs) != metrics.MaxErrorExamples || exs[0].Request != "GET /x?i=0" || exs[2].TraceID != "0102030405060708090a0b0c0d0e0f10" ||
		exs[0].RequestHeaders["Authorization"] != "[redacted]" || !exs[0].At.Equal(at) || exs[1].ResponseBody != "boom" {
		t.Fatalf("%+v", exs)
	}
}
