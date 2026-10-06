// Package report turns run metrics into a verdict, per-journey and
// per-step statistics, a timeline and exportable documents.
package report

import (
	"sort"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/version"
)

// Verdicts.
const (
	VerdictPass           = "pass"
	VerdictFail           = "fail"
	VerdictGeneratorLimit = "generator-limited"
	VerdictNoTargets      = "no-targets"
)

// Report is the complete result of a run. It is the JSON export format.
type Report struct {
	FormatVersion int       `json:"formatVersion"`
	Stampede      string    `json:"stampede"`
	RunID         string    `json:"runId,omitempty"`
	Scenario      string    `json:"scenario"`
	Target        string    `json:"target"`
	Started       time.Time `json:"started"`
	Ended         time.Time `json:"ended"`
	// Duration is the measured load phase in seconds.
	Duration   float64     `json:"duration"`
	StopReason string      `json:"stopReason"`
	Load       LoadInfo    `json:"load"`
	Verdict    string      `json:"verdict"`
	Thresholds []Check     `json:"thresholds"`
	Overall    Stats       `json:"overall"`
	Journeys   []Journey   `json:"journeys"`
	Errors     []ErrorRow  `json:"errors"`
	Timeline   []Point     `json:"timeline"`
	Breakpoint *Breakpoint `json:"breakpoint,omitempty"`
	// Curve and Knee are set for runs whose load changed over time.
	Curve []CurvePoint `json:"curve,omitempty"`
	Knee  *Knee        `json:"knee,omitempty"`
	Notes []string     `json:"notes,omitempty"`
	// Workers describes each worker of a distributed run (empty for a
	// single in-process engine).
	Workers []WorkerRow `json:"workers,omitempty"`
	// Narrative is an optional AI-written summary that cites the figures.
	Narrative *Narrative `json:"narrative,omitempty"`
	// TargetMetrics are the target's own metrics over the run, queried
	// from Prometheus after it ended (observe.prometheus).
	TargetMetrics []TargetMetric `json:"targetMetrics,omitempty"`
}

// TargetMetric is one Prometheus query evaluated over the run.
type TargetMetric struct {
	Name   string        `json:"name"`
	Query  string        `json:"query"`
	Points []MetricPoint `json:"points"`
	// Error explains why points are missing or incomplete.
	Error string `json:"error,omitempty"`
}

// MetricPoint is one value of a target metric.
type MetricPoint struct {
	// T is seconds since the run started, like Point.T.
	T     float64 `json:"t"`
	Value float64 `json:"value"`
}

// Range returns the minimum, maximum and last value; ok is false when
// there are no points.
func (m TargetMetric) Range() (lo, hi, last float64, ok bool) {
	if len(m.Points) == 0 {
		return 0, 0, 0, false
	}
	lo, hi = m.Points[0].Value, m.Points[0].Value
	for _, p := range m.Points {
		lo, hi = min(lo, p.Value), max(hi, p.Value)
	}
	return lo, hi, m.Points[len(m.Points)-1].Value, true
}

// SlowRequest is one of a step's slowest requests.
type SlowRequest struct {
	// Latency is in seconds, measured from the intended send time.
	Latency float64 `json:"latency"`
	// At is when the request was sent; T is the same in seconds since
	// the run started.
	At time.Time `json:"at"`
	T  float64   `json:"t"`
	// TraceID is the W3C trace ID the request carried in traceparent.
	TraceID string `json:"traceId,omitempty"`
	// TraceURL links to the trace when a trace link template is set.
	TraceURL string `json:"traceUrl,omitempty"`
	Status   int    `json:"status,omitempty"`
	Error    string `json:"error,omitempty"`
}

// SetTraceLinks fills TraceURL for every slow request from a template
// containing {traceId}. An empty template clears the links.
func (r *Report) SetTraceLinks(tmpl string) {
	for ji := range r.Journeys {
		for si := range r.Journeys[ji].Steps {
			sl := r.Journeys[ji].Steps[si].Slowest
			for i := range sl {
				sl[i].TraceURL = scenario.TraceLink(tmpl, sl[i].TraceID)
			}
		}
	}
}

// LoadInfo summarises the plan.
type LoadInfo struct {
	Shape    string  `json:"shape,omitempty"`
	Mode     string  `json:"mode"`
	Executor string  `json:"executor"`
	Peak     float64 `json:"peak"`
	PeakVUs  int     `json:"peakVUs"`
	Workers  int     `json:"workers"`
}

// Check is the outcome of one target.
type Check struct {
	Source   string  `json:"source"`
	Scope    string  `json:"scope"`
	Metric   string  `json:"metric"`
	Op       string  `json:"op"`
	Target   float64 `json:"target"`
	Observed float64 `json:"observed"`
	Pass     bool    `json:"pass"`
	// Display strings in the metric's unit.
	TargetText   string `json:"targetText"`
	ObservedText string `json:"observedText"`
}

// Stats summarises a set of requests.
type Stats struct {
	Requests     uint64              `json:"requests"`
	Failed       uint64              `json:"failed"`
	ErrorRate    float64             `json:"errorRate"`
	RPS          float64             `json:"rps"`
	Latency      metrics.Percentiles `json:"latency"`
	Service      metrics.Percentiles `json:"service"`
	BytesIn      uint64              `json:"bytesIn"`
	BytesOut     uint64              `json:"bytesOut"`
	ChecksPassed uint64              `json:"checksPassed"`
	ChecksFailed uint64              `json:"checksFailed"`
	Status       map[int]uint64      `json:"status,omitempty"`
	// Iteration-level figures (overall and per journey).
	Iterations       uint64              `json:"iterations,omitempty"`
	IterationsFailed uint64              `json:"iterationsFailed,omitempty"`
	IterationTime    metrics.Percentiles `json:"iterationTime,omitzero"`
	Dropped          uint64              `json:"dropped,omitempty"`
}

// Journey is per-journey output.
type Journey struct {
	Name  string `json:"name"`
	Stats Stats  `json:"stats"`
	Steps []Step `json:"steps"`
}

// Step is per-request-step output.
type Step struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Stats Stats  `json:"stats"`
	// Phases holds mean and p95 seconds for dns, connect, tls, wait and download.
	Phases map[string]PhaseStat `json:"phases"`
	// Protocols counts requests by negotiated protocol (HTTP/1.1, HTTP/2.0).
	Protocols map[string]uint64 `json:"protocols,omitempty"`
	// Stream is set for streaming steps (server-sent events, gRPC server
	// streaming).
	Stream *StreamStat `json:"stream,omitempty"`
	// Slowest lists the step's slowest requests, slowest first.
	Slowest []SlowRequest `json:"slowest,omitempty"`
}

// StreamStat summarises a streaming step. For an LLM API, FirstEvent is
// the time to first token and EventsPerSec is tokens per second.
type StreamStat struct {
	// Streams counts steps that received at least one event.
	Streams uint64 `json:"streams"`
	Events  uint64 `json:"events"`
	// EventsPerSec is the rate of the events after the first, measured
	// from each stream's first event to its end.
	EventsPerSec float64 `json:"eventsPerSec"`
	// FirstEvent is the time from the start of the step to its first
	// event, in seconds.
	FirstEvent FirstEventStat `json:"firstEvent"`
}

// FirstEventStat is a time-to-first-event summary in seconds.
type FirstEventStat struct {
	Mean float64 `json:"mean"`
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
	P99  float64 `json:"p99"`
}

// PhaseStat is a timing phase summary in seconds.
type PhaseStat struct {
	Mean float64 `json:"mean"`
	P95  float64 `json:"p95"`
}

// ErrorRow counts one kind of failure at one step.
type ErrorRow struct {
	Journey string `json:"journey"`
	Step    string `json:"step"`
	Error   string `json:"error"`
	Count   uint64 `json:"count"`
}

// Point is one timeline interval.
type Point struct {
	T          float64 `json:"t"` // seconds since start
	RPS        float64 `json:"rps"`
	ErrorRate  float64 `json:"errorRate"`
	P50        float64 `json:"p50"`
	P95        float64 `json:"p95"`
	P99        float64 `json:"p99"`
	VUs        int     `json:"vus"`
	Planned    float64 `json:"planned"`
	Dropped    uint64  `json:"dropped"`
	Iterations uint64  `json:"iterations"`
	SchedLag99 float64 `json:"schedLagP99"`
}

// Breakpoint is the outcome of a breakpoint search.
type Breakpoint struct {
	// Found is false when every level passed.
	Found bool `json:"found"`
	// LastPass is the highest planned load that met every target.
	LastPass float64 `json:"lastPass"`
	// FirstFail is the lowest planned load that missed a target.
	FirstFail float64  `json:"firstFail,omitempty"`
	Unit      string   `json:"unit"`
	FailedOn  []string `json:"failedOn,omitempty"`
}

// Input is everything needed to build a report.
type Input struct {
	RunID      string
	Program    *scenario.Program
	Plan       *scenario.Plan
	Target     string
	Started    time.Time
	Ended      time.Time
	StopReason string
	PeakVUs    int
	Workers    int
	Interval   time.Duration
	Snapshots  []*metrics.Snapshot // already merged per interval, any order
	Phases     map[int]*[metrics.NumPhases]*metrics.Histogram
	Breakpoint *Breakpoint
}

// Build computes a report.
func Build(in Input) *Report {
	if in.Interval <= 0 {
		in.Interval = time.Second
	}
	snaps := append([]*metrics.Snapshot(nil), in.Snapshots...)
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].Interval < snaps[j].Interval })

	total := metrics.NewSnapshot(0)
	for _, s := range snaps {
		total.Merge(s)
	}
	dur := in.Ended.Sub(in.Started).Seconds()
	if dur <= 0 {
		dur = float64(len(snaps)) * in.Interval.Seconds()
	}

	r := &Report{
		FormatVersion: 1,
		Stampede:      version.Version,
		RunID:         in.RunID,
		Scenario:      in.Program.Scenario.Metadata.Name,
		Target:        in.Target,
		Started:       in.Started,
		Ended:         in.Ended,
		Duration:      dur,
		StopReason:    in.StopReason,
		Load: LoadInfo{
			Shape: in.Plan.Shape, Mode: in.Plan.Mode, Executor: in.Plan.Executor,
			Peak: in.Plan.Peak(), PeakVUs: in.PeakVUs, Workers: max(in.Workers, 1),
		},
		Breakpoint: in.Breakpoint,
	}

	// Overall.
	r.Overall = statsOf(total.Totals(), dur)
	for _, j := range total.Journeys {
		r.Overall.Iterations += j.Completed + j.Failed
		r.Overall.IterationsFailed += j.Failed
	}
	r.Overall.Dropped = total.Dropped
	allIter := metrics.NewHistogram()
	for _, j := range total.Journeys {
		allIter.Merge(j.Duration)
	}
	r.Overall.IterationTime = allIter.Summary()

	// Journeys and steps.
	for _, cj := range in.Program.Journeys {
		jr := Journey{Name: cj.Name}
		jTotal := &metrics.StepStats{Latency: metrics.NewHistogram(), Service: metrics.NewHistogram()}
		for _, cs := range in.Program.Steps {
			if cs.Journey != cj.Name {
				continue
			}
			st := total.Steps[cs.ID]
			if st == nil {
				st = &metrics.StepStats{Latency: metrics.NewHistogram(), Service: metrics.NewHistogram()}
			}
			jTotal.Merge(st)
			sr := Step{ID: cs.ID, Name: cs.Name, Stats: statsOf(st, dur), Phases: map[string]PhaseStat{}, Protocols: st.Protocols}
			if st.Streams > 0 {
				sr.Stream = streamOf(st, in.Phases[cs.ID])
			}
			for _, sl := range st.Slowest {
				sr.Slowest = append(sr.Slowest, SlowRequest{
					Latency: sl.Latency.Seconds(), At: sl.Start, T: max(sl.Start.Sub(in.Started).Seconds(), 0),
					TraceID: sl.TraceID, Status: sl.Status, Error: sl.Err,
				})
			}
			// The first-event phase is reported under Stream, and only for
			// streaming steps.
			for p := metrics.Phase(0); p < metrics.PhaseFirstEvent; p++ {
				ps := PhaseStat{}
				if st.Requests > 0 {
					ps.Mean = float64(st.PhaseSum[p]) / float64(st.Requests) / 1e6
				}
				if ph := in.Phases[cs.ID]; ph != nil {
					ps.P95 = ph[p].QuantileSeconds(0.95)
				}
				sr.Phases[metrics.PhaseNames[p]] = ps
			}
			jr.Steps = append(jr.Steps, sr)
			for e, n := range st.Errors {
				r.Errors = append(r.Errors, ErrorRow{Journey: cj.Name, Step: cs.Name, Error: e, Count: n})
			}
		}
		jr.Stats = statsOf(jTotal, dur)
		if js := total.Journeys[cj.Index]; js != nil {
			jr.Stats.Iterations = js.Completed + js.Failed
			jr.Stats.IterationsFailed = js.Failed
			jr.Stats.IterationTime = js.Duration.Summary()
		}
		r.Journeys = append(r.Journeys, jr)
	}
	sort.Slice(r.Errors, func(i, j int) bool { return r.Errors[i].Count > r.Errors[j].Count })

	// Timeline.
	// The timeline covers the planned load phase. Iterations finishing
	// during the graceful stop still count in the totals above, but a
	// partial trailing interval would distort per-second rates.
	secs := in.Interval.Seconds()
	planned := in.Plan.TotalDuration().Seconds()
	for _, s := range snaps {
		if planned > 0 && float64(s.Interval)*secs >= planned && len(r.Timeline) > 0 {
			continue
		}
		t := s.Totals()
		p := Point{
			T:       float64(s.Interval) * secs,
			RPS:     float64(t.Requests) / secs,
			VUs:     s.VUs,
			Planned: s.Planned,
			Dropped: s.Dropped,
		}
		if t.Requests > 0 {
			p.ErrorRate = float64(t.Failed) / float64(t.Requests)
			q := t.Latency.Quantiles(0.5, 0.95, 0.99)
			p.P50, p.P95, p.P99 = float64(q[0])/1e6, float64(q[1])/1e6, float64(q[2])/1e6
		}
		for _, j := range s.Journeys {
			p.Iterations += j.Completed + j.Failed
		}
		if s.SchedLag != nil {
			p.SchedLag99 = s.SchedLag.QuantileSeconds(0.99)
		}
		r.Timeline = append(r.Timeline, p)
	}

	if len(in.Plan.Stages) > 0 {
		r.Curve = buildCurve(snaps, secs, planned)
		r.Knee = findKnee(r.Curve, in.Plan.Mode)
	}

	// Targets.
	r.Thresholds = Evaluate(in.Program, in.Program.Thresholds, total, dur)
	r.Verdict = VerdictNoTargets
	if len(r.Thresholds) > 0 {
		r.Verdict = VerdictPass
		for _, c := range r.Thresholds {
			if !c.Pass {
				r.Verdict = VerdictFail
			}
		}
	}
	// A breakpoint run pushes past the targets on purpose, so judging the
	// whole run against them would always fail it. Targets are judged per
	// level instead: the run passes when at least one level held.
	if bp := in.Breakpoint; bp != nil && len(r.Thresholds) > 0 {
		if bp.LastPass > 0 {
			r.Verdict = VerdictPass
			r.Notes = append(r.Notes, "In a breakpoint run targets are judged per load level; the targets table covers the whole run, including the levels past the breakpoint.")
		} else {
			r.Verdict = VerdictFail
			r.Notes = append(r.Notes, "Targets failed at the first load level of the breakpoint search.")
		}
	}
	if in.StopReason != "" && in.StopReason != "completed" && in.StopReason != "breakpoint reached" {
		r.Notes = append(r.Notes, "Run ended early: "+in.StopReason+".")
	}
	if total.Dropped > 0 {
		r.Notes = append(r.Notes, "Some iterations were dropped because no virtual user was free. Raise maxVUs or reduce the rate; dropped iterations show the generator could not keep the schedule.")
	}
	return r
}

func streamOf(st *metrics.StepStats, ph *[metrics.NumPhases]*metrics.Histogram) *StreamStat {
	ss := &StreamStat{Streams: st.Streams, Events: st.Events}
	ss.FirstEvent.Mean = float64(st.PhaseSum[metrics.PhaseFirstEvent]) / float64(st.Streams) / 1e6
	if st.StreamUs > 0 && st.Events > st.Streams {
		ss.EventsPerSec = float64(st.Events-st.Streams) / (float64(st.StreamUs) / 1e6)
	}
	if ph != nil {
		h := ph[metrics.PhaseFirstEvent]
		ss.FirstEvent.P50 = h.QuantileSeconds(0.5)
		ss.FirstEvent.P95 = h.QuantileSeconds(0.95)
		ss.FirstEvent.P99 = h.QuantileSeconds(0.99)
	}
	return ss
}

func statsOf(st *metrics.StepStats, dur float64) Stats {
	s := Stats{
		Requests: st.Requests, Failed: st.Failed,
		Latency: st.Latency.Summary(), Service: st.Service.Summary(),
		BytesIn: st.BytesIn, BytesOut: st.BytesOut,
		ChecksPassed: st.ChecksPassed, ChecksFailed: st.ChecksFailed,
		Status: st.Status,
	}
	if st.Requests > 0 {
		s.ErrorRate = float64(st.Failed) / float64(st.Requests)
	}
	if dur > 0 {
		s.RPS = float64(st.Requests) / dur
	}
	return s
}
