package metrics

import (
	"encoding/base64"
	"time"
)

// Phase indexes per-request timing phases.
type Phase int

// Request phases, as measured by net/http/httptrace, plus the time to the
// first event of a stream (server-sent events, gRPC server streaming).
// New phases are only ever appended: workers and servers exchange phases
// by index, and a peer that knows fewer phases leaves the rest empty.
const (
	PhaseDNS Phase = iota
	PhaseConnect
	PhaseTLS
	PhaseWait // time to first byte after the request was written
	PhaseDownload
	// PhaseFirstEvent is the time from the start of a streaming step to
	// its first event: time to first token for an LLM API.
	PhaseFirstEvent
	NumPhases
)

// PhaseNames are the report labels for each phase.
var PhaseNames = [NumPhases]string{"dns", "connect", "tls", "wait", "download", "firstEvent"}

// Sample is one completed request.
type Sample struct {
	Step int
	// Intended is when the request should have been sent. In open-model
	// runs a late dispatch makes it earlier than Start; latency is
	// measured from here to avoid coordinated omission.
	Intended time.Time
	Start    time.Time
	End      time.Time
	Phases   [NumPhases]time.Duration
	Status   int
	// Err is an error class ("" for success at the transport level).
	Err          string
	Failed       bool
	ChecksPassed int
	ChecksFailed int
	BytesIn      int64
	BytesOut     int64
	// Proto is the negotiated protocol, such as "HTTP/1.1" or "HTTP/2.0".
	Proto string
	// Events counts the events or messages a streaming step received;
	// StreamTime is the time from the first of them to the end of the step.
	Events     int
	StreamTime time.Duration
}

// Snapshot holds everything recorded during one interval (normally one
// second) by one worker, or the merge of many.
type Snapshot struct {
	// Interval is the number of whole intervals since the run's T0.
	Interval int64 `json:"interval"`
	// Seq increases by one per snapshot from a worker so resends are
	// detected and never counted twice.
	Seq      uint64                `json:"seq,omitempty"`
	Worker   string                `json:"worker,omitempty"`
	Steps    map[int]*StepStats    `json:"steps,omitempty"`
	Journeys map[int]*JourneyStats `json:"journeys,omitempty"`
	// Dropped counts open-model iterations that could not start because
	// no virtual user was free.
	Dropped uint64 `json:"dropped,omitempty"`
	// SchedLag is how late iterations were dispatched versus schedule.
	SchedLag *Histogram `json:"schedLag,omitempty"`
	// VUs is the number of active virtual users at the end of the interval.
	VUs int `json:"vus"`
	// Planned is the planned load (users or rate) at the interval.
	Planned float64 `json:"planned,omitempty"`
}

// StepStats aggregates one request step.
type StepStats struct {
	Requests     uint64            `json:"requests"`
	Failed       uint64            `json:"failed"`
	Errors       map[string]uint64 `json:"errors,omitempty"`
	Status       map[int]uint64    `json:"status,omitempty"`
	ChecksPassed uint64            `json:"checksPassed,omitempty"`
	ChecksFailed uint64            `json:"checksFailed,omitempty"`
	BytesIn      uint64            `json:"bytesIn"`
	BytesOut     uint64            `json:"bytesOut"`
	// Latency is measured from the intended send time.
	Latency *Histogram `json:"latency"`
	// Service is measured from the actual send time.
	Service *Histogram `json:"service"`
	// PhaseSum holds per-phase totals in µs for computing means.
	PhaseSum [NumPhases]uint64 `json:"phaseSum"`
	// Streams counts samples that received at least one stream event,
	// Events the events they received and StreamUs the µs from each
	// stream's first event to its end. Events per second after the first
	// event is (Events - Streams) / StreamUs.
	Streams  uint64 `json:"streams,omitempty"`
	Events   uint64 `json:"events,omitempty"`
	StreamUs uint64 `json:"streamUs,omitempty"`
	// Protocols counts requests by negotiated protocol (HTTP/1.1, HTTP/2.0).
	Protocols map[string]uint64 `json:"protocols,omitempty"`
}

// JourneyStats aggregates whole iterations of a journey.
type JourneyStats struct {
	Started   uint64     `json:"started"`
	Completed uint64     `json:"completed"`
	Failed    uint64     `json:"failed"`
	Duration  *Histogram `json:"duration"`
}

// NewSnapshot returns an empty snapshot for an interval.
func NewSnapshot(interval int64) *Snapshot {
	return &Snapshot{
		Interval: interval,
		Steps:    map[int]*StepStats{},
		Journeys: map[int]*JourneyStats{},
		SchedLag: NewHistogram(),
	}
}

// Step returns the stats for a step, creating them when absent.
func (s *Snapshot) Step(id int) *StepStats {
	st := s.Steps[id]
	if st == nil {
		st = &StepStats{Latency: NewHistogram(), Service: NewHistogram()}
		s.Steps[id] = st
	}
	return st
}

// Journey returns the stats for a journey, creating them when absent.
func (s *Snapshot) Journey(id int) *JourneyStats {
	j := s.Journeys[id]
	if j == nil {
		j = &JourneyStats{Duration: NewHistogram()}
		s.Journeys[id] = j
	}
	return j
}

// Add records a sample.
func (st *StepStats) Add(s *Sample) {
	st.Requests++
	if s.Failed {
		st.Failed++
	}
	if s.Err != "" {
		if st.Errors == nil {
			st.Errors = map[string]uint64{}
		}
		st.Errors[s.Err]++
	}
	if s.Status > 0 {
		if st.Status == nil {
			st.Status = map[int]uint64{}
		}
		st.Status[s.Status]++
	}
	st.ChecksPassed += uint64(s.ChecksPassed)
	st.ChecksFailed += uint64(s.ChecksFailed)
	st.BytesIn += uint64(max(s.BytesIn, 0))
	st.BytesOut += uint64(max(s.BytesOut, 0))
	intended := s.Intended
	if intended.IsZero() || intended.After(s.Start) {
		intended = s.Start
	}
	st.Latency.RecordDuration(s.End.Sub(intended))
	st.Service.RecordDuration(s.End.Sub(s.Start))
	for i, d := range s.Phases {
		if d > 0 {
			st.PhaseSum[i] += uint64(d / time.Microsecond)
		}
	}
	if s.Events > 0 {
		st.Streams++
		st.Events += uint64(s.Events)
		st.StreamUs += uint64(max(s.StreamTime, 0) / time.Microsecond)
	}
	if s.Proto != "" {
		if st.Protocols == nil {
			st.Protocols = map[string]uint64{}
		}
		st.Protocols[s.Proto]++
	}
}

// Merge adds o into st.
func (st *StepStats) Merge(o *StepStats) {
	st.Requests += o.Requests
	st.Failed += o.Failed
	for k, v := range o.Errors {
		if st.Errors == nil {
			st.Errors = map[string]uint64{}
		}
		st.Errors[k] += v
	}
	for k, v := range o.Status {
		if st.Status == nil {
			st.Status = map[int]uint64{}
		}
		st.Status[k] += v
	}
	st.ChecksPassed += o.ChecksPassed
	st.ChecksFailed += o.ChecksFailed
	st.BytesIn += o.BytesIn
	st.BytesOut += o.BytesOut
	st.Latency.Merge(o.Latency)
	st.Service.Merge(o.Service)
	for i := range st.PhaseSum {
		st.PhaseSum[i] += o.PhaseSum[i]
	}
	st.Streams += o.Streams
	st.Events += o.Events
	st.StreamUs += o.StreamUs
	for k, v := range o.Protocols {
		if st.Protocols == nil {
			st.Protocols = map[string]uint64{}
		}
		st.Protocols[k] += v
	}
}

// Merge adds o into j.
func (j *JourneyStats) Merge(o *JourneyStats) {
	j.Started += o.Started
	j.Completed += o.Completed
	j.Failed += o.Failed
	j.Duration.Merge(o.Duration)
}

// Merge adds every count and histogram in o into s. Gauges (VUs) are
// summed, which is right when merging different workers for the same
// interval.
func (s *Snapshot) Merge(o *Snapshot) {
	for id, st := range o.Steps {
		s.Step(id).Merge(st)
	}
	for id, j := range o.Journeys {
		s.Journey(id).Merge(j)
	}
	s.Dropped += o.Dropped
	if s.SchedLag == nil {
		s.SchedLag = NewHistogram()
	}
	s.SchedLag.Merge(o.SchedLag)
	s.VUs += o.VUs
	if o.Planned > s.Planned {
		s.Planned = o.Planned
	}
}

// Totals sums all steps of the snapshot into one StepStats.
func (s *Snapshot) Totals() *StepStats {
	t := &StepStats{Latency: NewHistogram(), Service: NewHistogram()}
	for _, id := range sortedKeys(s.Steps) {
		t.Merge(s.Steps[id])
	}
	return t
}

// Empty reports whether nothing was recorded.
func (s *Snapshot) Empty() bool {
	return len(s.Steps) == 0 && len(s.Journeys) == 0 && s.Dropped == 0 && (s.SchedLag == nil || s.SchedLag.Count() == 0)
}

// MarshalText encodes the histogram as base64 of its binary form, so it
// can travel inside JSON.
func (h *Histogram) MarshalText() ([]byte, error) {
	b, err := h.MarshalBinary()
	if err != nil {
		return nil, err
	}
	out := make([]byte, base64.StdEncoding.EncodedLen(len(b)))
	base64.StdEncoding.Encode(out, b)
	return out, nil
}

// UnmarshalText decodes MarshalText output.
func (h *Histogram) UnmarshalText(text []byte) error {
	b := make([]byte, base64.StdEncoding.DecodedLen(len(text)))
	n, err := base64.StdEncoding.Decode(b, text)
	if err != nil {
		return err
	}
	return h.UnmarshalBinary(b[:n])
}
