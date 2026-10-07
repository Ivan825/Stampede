package runner

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Ivan825/Stampede/internal/health"
	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/wire"
)

// SelfMonitor watches the machine running an in-process engine (`stampede
// run`, or the server running load itself), as workers watch themselves,
// so a report can say when the generator rather than the target was the
// limit.
type SelfMonitor struct {
	mon  *health.Monitor
	done chan struct{}
	once sync.Once

	mu        sync.Mutex
	saturated []int64
	reasons   map[string]bool
	// last is the latest sample and when it was taken.
	last   wire.Health
	lastAt time.Time
}

// Latest is the most recent health sample and when it was taken; the
// time is zero before the first interval.
func (m *SelfMonitor) Latest() (wire.Health, time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.last
	h.Reasons = append([]string(nil), h.Reasons...)
	return h, m.lastAt
}

// NewSelfMonitor starts sampling the process once a second; Close stops it.
func NewSelfMonitor() *SelfMonitor {
	m := &SelfMonitor{mon: health.NewMonitor(health.DefaultThresholds), done: make(chan struct{}), reasons: map[string]bool{}}
	go m.mon.Loop(m.done)
	return m
}

// Observe takes each interval's snapshot, in order.
func (m *SelfMonitor) Observe(s *metrics.Snapshot) {
	var lag time.Duration
	if s.SchedLag != nil {
		lag = time.Duration(s.SchedLag.Quantile(0.99)) * time.Microsecond
	}
	h := m.mon.Observe(lag, s.Dropped)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.last, m.lastAt = h, time.Now()
	if !h.Saturated {
		return
	}
	m.saturated = append(m.saturated, s.Interval)
	for _, r := range h.Reasons {
		m.reasons[health.Kind(r)] = true
	}
}

// Close stops sampling.
func (m *SelfMonitor) Close() { m.once.Do(func() { close(m.done) }) }

// Annotate, when the generator was saturated, adds it as the report's one
// worker with its saturated windows, and the same notes and verdict a
// distributed run gets. A healthy run's report is unchanged.
func (m *SelfMonitor) Annotate(rep *report.Report, interval time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	secs := interval.Seconds()
	if secs <= 0 {
		secs = 1
	}
	row := report.WorkerRow{ID: "local", Name: "this machine", ShareLo: 0, ShareHi: 1, State: "finished", PeakVUs: rep.Load.PeakVUs, Requests: rep.Overall.Requests}
	for _, w := range spans(m.saturated) {
		row.Saturated = append(row.Saturated, report.Span{From: float64(w[0]) * secs, To: float64(w[1]+1) * secs})
	}
	for k := range m.reasons {
		row.SaturationReasons = append(row.SaturationReasons, k)
	}
	sort.Strings(row.SaturationReasons)
	if len(row.Saturated) > 0 {
		rep.Workers = append(rep.Workers, row)
		noteSaturated(rep, "The load generator (this machine)", row)
		generatorLimited(rep)
	}
}

// noteSaturated explains a worker's saturated windows in the notes.
func noteSaturated(rep *report.Report, who string, row report.WorkerRow) {
	var total float64
	var parts []string
	for _, s := range row.Saturated {
		total += s.To - s.From
		parts = append(parts, fmtSecs(s.From)+"–"+fmtSecs(s.To))
	}
	rep.Notes = append(rep.Notes, fmt.Sprintf(
		"%s was saturated for %s (%s: %s). Latency measured in those windows may reflect the load generator rather than the target; add workers or capacity.",
		who, fmtSecs(total), strings.Join(parts, ", "), strings.Join(row.SaturationReasons, ", ")))
}

// generatorLimited turns a failed verdict measured by a saturated
// generator into generator-limited.
func generatorLimited(rep *report.Report) {
	if rep.Verdict == report.VerdictFail {
		rep.Verdict = report.VerdictGeneratorLimit
		rep.Notes = append(rep.Notes, "Targets failed while the load generator was saturated, so the failure may be the generator's; the verdict is generator-limited rather than fail.")
	}
}

// spans compresses interval numbers into inclusive [from, to] ranges.
func spans(ks []int64) [][2]int64 {
	if len(ks) == 0 {
		return nil
	}
	ks = append([]int64(nil), ks...)
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	out := [][2]int64{{ks[0], ks[0]}}
	for _, k := range ks[1:] {
		if last := &out[len(out)-1]; k <= last[1]+1 {
			last[1] = max(last[1], k)
		} else {
			out = append(out, [2]int64{k, k})
		}
	}
	return out
}
