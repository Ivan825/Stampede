// Package wire converts between Stampede's in-memory metrics and the
// worker protocol messages, and holds the protocol version both sides
// negotiate. Keeping the conversions in one place means the worker and
// the coordinator cannot disagree on how a snapshot is encoded.
package wire

import (
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	workerv1 "github.com/Ivan825/Stampede/gen/stampede/worker/v1"
	"github.com/Ivan825/Stampede/internal/metrics"
)

// Protocol version spoken by this build. Minor versions only add fields,
// so any minor of the same major interoperates.
const (
	ProtocolMajor = 1
	ProtocolMinor = 2
)

// Version is the protocol version as a message.
func Version() *workerv1.ProtocolVersion {
	return &workerv1.ProtocolVersion{Major: ProtocolMajor, Minor: ProtocolMinor}
}

// CheckVersion returns an error explaining why a peer speaking v cannot
// be served. A missing version is treated as incompatible: every build
// that speaks this protocol sends one.
func CheckVersion(v *workerv1.ProtocolVersion) error {
	if v == nil {
		return fmt.Errorf("worker sent no protocol version; this server speaks protocol v%d.%d, upgrade the worker to a matching stampede release", ProtocolMajor, ProtocolMinor)
	}
	if v.GetMajor() != ProtocolMajor {
		return fmt.Errorf("worker speaks protocol v%d.%d but this server speaks v%d.%d; run the same stampede major release on the server and its workers",
			v.GetMajor(), v.GetMinor(), ProtocolMajor, ProtocolMinor)
	}
	return nil
}

// Health is a worker's self-monitoring for one sample (normally one
// second). Saturated means the worker itself, not the target, may be
// what limits the measured load.
type Health struct {
	Saturated   bool          `json:"saturated"`
	Reasons     []string      `json:"reasons,omitempty"`
	CPUPercent  float64       `json:"cpuPercent"`
	SchedLagP99 time.Duration `json:"schedLagP99"`
	GCPauseP99  time.Duration `json:"gcPauseP99"`
	OpenFDs     uint64        `json:"openFDs"`
	FDLimit     uint64        `json:"fdLimit"`
	Goroutines  int           `json:"goroutines"`
	Dropped     uint64        `json:"dropped"`
}

// HealthToProto encodes h. A nil h encodes as nil.
func HealthToProto(h *Health) *workerv1.Health {
	if h == nil {
		return nil
	}
	return &workerv1.Health{
		Saturated:     h.Saturated,
		Reasons:       append([]string(nil), h.Reasons...),
		CpuPercent:    h.CPUPercent,
		SchedLagP99Ns: int64(h.SchedLagP99),
		GcPauseP99Ns:  int64(h.GCPauseP99),
		OpenFds:       h.OpenFDs,
		FdLimit:       h.FDLimit,
		Goroutines:    uint32(max(h.Goroutines, 0)),
		Dropped:       h.Dropped,
	}
}

// HealthFromProto decodes p. A nil p decodes as nil.
func HealthFromProto(p *workerv1.Health) *Health {
	if p == nil {
		return nil
	}
	return &Health{
		Saturated:   p.GetSaturated(),
		Reasons:     append([]string(nil), p.GetReasons()...),
		CPUPercent:  p.GetCpuPercent(),
		SchedLagP99: time.Duration(p.GetSchedLagP99Ns()),
		GCPauseP99:  time.Duration(p.GetGcPauseP99Ns()),
		OpenFDs:     p.GetOpenFds(),
		FDLimit:     p.GetFdLimit(),
		Goroutines:  int(p.GetGoroutines()),
		Dropped:     p.GetDropped(),
	}
}

// SnapshotToProto encodes one worker snapshot for a run.
func SnapshotToProto(runID string, s *metrics.Snapshot, h *Health) (*workerv1.Snapshot, error) {
	p := &workerv1.Snapshot{
		RunId:    runID,
		Seq:      s.Seq,
		Interval: s.Interval,
		Dropped:  s.Dropped,
		Vus:      uint32(max(s.VUs, 0)),
		Planned:  s.Planned,
		Health:   HealthToProto(h),
	}
	var err error
	if p.SchedLag, err = histBytes(s.SchedLag); err != nil {
		return nil, err
	}
	if len(s.Steps) > 0 {
		p.Steps = make(map[int32]*workerv1.StepStats, len(s.Steps))
	}
	for id, st := range s.Steps {
		ps := &workerv1.StepStats{
			Requests: st.Requests, Failed: st.Failed,
			ChecksPassed: st.ChecksPassed, ChecksFailed: st.ChecksFailed,
			BytesIn: st.BytesIn, BytesOut: st.BytesOut,
			PhaseSum: append([]uint64(nil), st.PhaseSum[:]...),
			Streams:  st.Streams, Events: st.Events, StreamUs: st.StreamUs,
		}
		if len(st.Protocols) > 0 {
			ps.Protocols = make(map[string]uint64, len(st.Protocols))
			for k, v := range st.Protocols {
				ps.Protocols[k] = v
			}
		}
		for _, r := range st.Slowest {
			ps.Slowest = append(ps.Slowest, slowToProto(r))
		}
		if len(st.Errors) > 0 {
			ps.Errors = make(map[string]uint64, len(st.Errors))
			for k, v := range st.Errors {
				ps.Errors[k] = v
			}
		}
		if len(st.Status) > 0 {
			ps.Status = make(map[int32]uint64, len(st.Status))
			for k, v := range st.Status {
				ps.Status[int32(k)] = v
			}
		}
		if ps.Latency, err = histBytes(st.Latency); err != nil {
			return nil, err
		}
		if ps.Service, err = histBytes(st.Service); err != nil {
			return nil, err
		}
		p.Steps[int32(id)] = ps
	}
	if len(s.Journeys) > 0 {
		p.Journeys = make(map[int32]*workerv1.JourneyStats, len(s.Journeys))
	}
	for id, j := range s.Journeys {
		pj := &workerv1.JourneyStats{Started: j.Started, Completed: j.Completed, Failed: j.Failed}
		if pj.Duration, err = histBytes(j.Duration); err != nil {
			return nil, err
		}
		p.Journeys[int32(id)] = pj
	}
	return p, nil
}

// SnapshotFromProto decodes a snapshot sent by worker. The returned
// snapshot's Worker field is set to worker.
func SnapshotFromProto(worker string, p *workerv1.Snapshot) (*metrics.Snapshot, *Health, error) {
	s := metrics.NewSnapshot(p.GetInterval())
	s.Seq = p.GetSeq()
	s.Worker = worker
	s.Dropped = p.GetDropped()
	s.VUs = int(p.GetVus())
	s.Planned = p.GetPlanned()
	if err := s.SchedLag.UnmarshalBinary(p.GetSchedLag()); err != nil {
		return nil, nil, fmt.Errorf("sched lag: %w", err)
	}
	for id, ps := range p.GetSteps() {
		st := s.Step(int(id))
		st.Requests, st.Failed = ps.GetRequests(), ps.GetFailed()
		st.ChecksPassed, st.ChecksFailed = ps.GetChecksPassed(), ps.GetChecksFailed()
		st.BytesIn, st.BytesOut = ps.GetBytesIn(), ps.GetBytesOut()
		if len(ps.GetPhaseSum()) > len(st.PhaseSum) {
			return nil, nil, fmt.Errorf("step %d: %d phase sums, want at most %d", id, len(ps.GetPhaseSum()), len(st.PhaseSum))
		}
		copy(st.PhaseSum[:], ps.GetPhaseSum())
		st.Streams, st.Events, st.StreamUs = ps.GetStreams(), ps.GetEvents(), ps.GetStreamUs()
		for k, v := range ps.GetProtocols() {
			if st.Protocols == nil {
				st.Protocols = map[string]uint64{}
			}
			st.Protocols[k] = v
		}
		for k, v := range ps.GetErrors() {
			if st.Errors == nil {
				st.Errors = map[string]uint64{}
			}
			st.Errors[k] = v
		}
		for k, v := range ps.GetStatus() {
			if st.Status == nil {
				st.Status = map[int]uint64{}
			}
			st.Status[int(k)] = v
		}
		if len(ps.GetSlowest()) > metrics.MaxSlowest {
			return nil, nil, fmt.Errorf("step %d: %d slow requests, want at most %d", id, len(ps.GetSlowest()), metrics.MaxSlowest)
		}
		for _, r := range ps.GetSlowest() {
			sr, err := slowFromProto(r)
			if err != nil {
				return nil, nil, fmt.Errorf("step %d: %w", id, err)
			}
			st.Slowest = append(st.Slowest, sr)
		}
		if err := st.Latency.UnmarshalBinary(ps.GetLatency()); err != nil {
			return nil, nil, fmt.Errorf("step %d latency: %w", id, err)
		}
		if err := st.Service.UnmarshalBinary(ps.GetService()); err != nil {
			return nil, nil, fmt.Errorf("step %d service: %w", id, err)
		}
	}
	for id, pj := range p.GetJourneys() {
		j := s.Journey(int(id))
		j.Started, j.Completed, j.Failed = pj.GetStarted(), pj.GetCompleted(), pj.GetFailed()
		if err := j.Duration.UnmarshalBinary(pj.GetDuration()); err != nil {
			return nil, nil, fmt.Errorf("journey %d duration: %w", id, err)
		}
	}
	return s, HealthFromProto(p.GetHealth()), nil
}

// PhasesToProto encodes run-wide per-step phase histograms.
func PhasesToProto(ph map[int]*[metrics.NumPhases]*metrics.Histogram) (map[int32]*workerv1.PhaseHistograms, error) {
	if len(ph) == 0 {
		return nil, nil
	}
	out := make(map[int32]*workerv1.PhaseHistograms, len(ph))
	for id, hs := range ph {
		p := &workerv1.PhaseHistograms{Phases: make([][]byte, metrics.NumPhases)}
		for i, h := range hs {
			b, err := histBytes(h)
			if err != nil {
				return nil, err
			}
			p.Phases[i] = b
		}
		out[int32(id)] = p
	}
	return out, nil
}

// PhasesFromProto decodes PhasesToProto output.
func PhasesFromProto(p map[int32]*workerv1.PhaseHistograms) (map[int]*[metrics.NumPhases]*metrics.Histogram, error) {
	out := make(map[int]*[metrics.NumPhases]*metrics.Histogram, len(p))
	for id, hs := range p {
		if len(hs.GetPhases()) > int(metrics.NumPhases) {
			return nil, fmt.Errorf("step %d: %d phase histograms, want at most %d", id, len(hs.GetPhases()), metrics.NumPhases)
		}
		dst := &[metrics.NumPhases]*metrics.Histogram{}
		for i := range dst {
			dst[i] = metrics.NewHistogram()
		}
		for i, b := range hs.GetPhases() {
			if err := dst[i].UnmarshalBinary(b); err != nil {
				return nil, fmt.Errorf("step %d phase %s: %w", id, metrics.PhaseNames[i], err)
			}
		}
		out[int(id)] = dst
	}
	return out, nil
}

// MergePhases adds src into dst, creating entries as needed.
func MergePhases(dst, src map[int]*[metrics.NumPhases]*metrics.Histogram) {
	ids := make([]int, 0, len(src))
	for id := range src {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		d := dst[id]
		if d == nil {
			d = &[metrics.NumPhases]*metrics.Histogram{}
			for i := range d {
				d[i] = metrics.NewHistogram()
			}
			dst[id] = d
		}
		for i, h := range src[id] {
			d[i].Merge(h)
		}
	}
}

func slowToProto(r metrics.SlowRequest) *workerv1.SlowRequest {
	p := &workerv1.SlowRequest{
		LatencyNs: int64(r.Latency), StartUnixNano: r.Start.UnixNano(),
		Status: int32(r.Status), Error: r.Err, //nolint:gosec // HTTP status codes fit
	}
	if id, err := hex.DecodeString(r.TraceID); err == nil && len(id) == 16 {
		p.TraceId = id
	}
	return p
}

func slowFromProto(p *workerv1.SlowRequest) (metrics.SlowRequest, error) {
	r := metrics.SlowRequest{
		Latency: time.Duration(p.GetLatencyNs()), Start: time.Unix(0, p.GetStartUnixNano()),
		Status: int(p.GetStatus()), Err: p.GetError(),
	}
	switch id := p.GetTraceId(); len(id) {
	case 0:
	case 16:
		r.TraceID = metrics.TraceIDString([16]byte(id))
	default:
		return r, fmt.Errorf("slow request trace id has %d bytes, want 16", len(id))
	}
	return r, nil
}

// histBytes encodes h; a nil histogram encodes as empty, which decodes
// back to an empty histogram.
func histBytes(h *metrics.Histogram) ([]byte, error) {
	if h == nil {
		return nil, nil
	}
	return h.MarshalBinary()
}
