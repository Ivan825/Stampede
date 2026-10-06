package worker

import (
	"fmt"
	"math"
	"runtime"
	"runtime/metrics"
	"sync"
	"time"

	"github.com/Ivan825/Stampede/internal/wire"
)

// Thresholds decide when a worker reports itself saturated. A saturated
// worker may be the bottleneck, so latencies it measured in that window
// say as much about the generator as about the target.
type Thresholds struct {
	// CPUPercent of all cores, sustained for CPUFor.
	CPUPercent float64
	CPUFor     time.Duration
	// SchedLagP99 is the p99 of how late iterations were dispatched.
	SchedLagP99 time.Duration
	// GCPauseP99 is the p99 stop-the-world GC pause in the last sample.
	GCPauseP99 time.Duration
	// FDFraction of the open-file limit.
	FDFraction float64
	// Any dropped iteration (no virtual user free) always saturates.
}

// DefaultThresholds are the limits from the distributed-execution plan.
var DefaultThresholds = Thresholds{
	CPUPercent:  85,
	CPUFor:      5 * time.Second,
	SchedLagP99: 10 * time.Millisecond,
	GCPauseP99:  5 * time.Millisecond,
	FDFraction:  0.8,
}

// sysSample is one reading of the process.
type sysSample struct {
	cpuPercent float64
	cpuOK      bool
	gcPauseP99 time.Duration
	openFDs    uint64
	fdLimit    uint64
	goroutines int
}

// lagIntervals is how many consecutive intervals scheduling lag must stay
// above its threshold to count as saturation. A single interval at the
// start of a run (connections opening, goroutines warming up) is not.
const lagIntervals = 2

// evaluate turns a reading into a Health verdict. cpuHighFor is how long
// CPU has stayed above the threshold, including this sample; lagStreak is
// how many consecutive intervals scheduling lag has been above its limit.
func evaluate(th Thresholds, s sysSample, cpuHighFor, schedLag time.Duration, lagStreak int, dropped uint64) wire.Health {
	h := wire.Health{
		CPUPercent: s.cpuPercent, SchedLagP99: schedLag, GCPauseP99: s.gcPauseP99,
		OpenFDs: s.openFDs, FDLimit: s.fdLimit, Goroutines: s.goroutines, Dropped: dropped,
	}
	if s.cpuOK && s.cpuPercent > th.CPUPercent && cpuHighFor >= th.CPUFor {
		h.Reasons = append(h.Reasons, fmt.Sprintf("cpu %.0f%% above %.0f%% for %s", s.cpuPercent, th.CPUPercent, cpuHighFor.Round(time.Second)))
	}
	if schedLag > th.SchedLagP99 && lagStreak >= lagIntervals {
		h.Reasons = append(h.Reasons, fmt.Sprintf("scheduling lag p99 %s above %s", schedLag.Round(time.Microsecond*100), th.SchedLagP99))
	}
	if dropped > 0 {
		h.Reasons = append(h.Reasons, fmt.Sprintf("%d iterations dropped (no virtual user free)", dropped))
	}
	if s.gcPauseP99 > th.GCPauseP99 {
		h.Reasons = append(h.Reasons, fmt.Sprintf("GC pause p99 %s above %s", s.gcPauseP99.Round(time.Microsecond*100), th.GCPauseP99))
	}
	if s.fdLimit > 0 && float64(s.openFDs) > th.FDFraction*float64(s.fdLimit) {
		h.Reasons = append(h.Reasons, fmt.Sprintf("%d of %d file descriptors open", s.openFDs, s.fdLimit))
	}
	h.Saturated = len(h.Reasons) > 0
	return h
}

// monitor samples the process once a second. The engine-level signals
// (scheduling lag, dropped iterations) come from each snapshot.
type monitor struct {
	th Thresholds

	mu        sync.Mutex
	last      sysSample
	cpuHigh   time.Duration // how long CPU has been above threshold
	prevCPU   time.Duration
	prevWall  time.Time
	prevGC    []uint64
	gcSample  []metrics.Sample
	lastLag   time.Duration
	lagStreak int
	lastDrops uint64
}

const gcPauses = "/sched/pauses/total/gc:seconds"

func newMonitor(th Thresholds) *monitor {
	m := &monitor{th: th, gcSample: []metrics.Sample{{Name: gcPauses}}}
	m.prevCPU, _ = processCPU()
	m.prevWall = time.Now()
	metrics.Read(m.gcSample)
	if m.gcSample[0].Value.Kind() == metrics.KindFloat64Histogram {
		m.prevGC = append([]uint64(nil), m.gcSample[0].Value.Float64Histogram().Counts...)
	}
	return m
}

// loop samples every second until done is closed.
func (m *monitor) loop(done <-chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			m.sample()
		}
	}
}

func (m *monitor) sample() {
	now := time.Now()
	s := sysSample{goroutines: runtime.NumGoroutine()}
	cpu, ok := processCPU()

	m.mu.Lock()
	defer m.mu.Unlock()
	if wall := now.Sub(m.prevWall); ok && wall > 0 {
		s.cpuOK = true
		s.cpuPercent = 100 * float64(cpu-m.prevCPU) / (float64(wall) * float64(runtime.NumCPU()))
		if s.cpuPercent > m.th.CPUPercent {
			m.cpuHigh += wall
		} else {
			m.cpuHigh = 0
		}
	}
	m.prevCPU, m.prevWall = cpu, now
	s.gcPauseP99 = m.gcP99()
	s.openFDs, s.fdLimit, _ = fdUsage()
	m.last = s
}

// gcP99 returns the p99 GC pause since the previous call, using the
// upper bound of the bucket that holds it.
func (m *monitor) gcP99() time.Duration {
	metrics.Read(m.gcSample)
	v := m.gcSample[0].Value
	if v.Kind() != metrics.KindFloat64Histogram {
		return 0
	}
	h := v.Float64Histogram()
	delta := make([]uint64, len(h.Counts))
	var total uint64
	for i, c := range h.Counts {
		if i < len(m.prevGC) {
			c -= m.prevGC[i]
		}
		delta[i] = c
		total += c
	}
	m.prevGC = append(m.prevGC[:0], h.Counts...)
	if total == 0 {
		return 0
	}
	rank := uint64(math.Ceil(0.99 * float64(total)))
	var seen uint64
	for i, c := range delta {
		seen += c
		if seen >= rank {
			hi := h.Buckets[i+1]
			if math.IsInf(hi, 1) {
				hi = h.Buckets[i]
			}
			return time.Duration(hi * float64(time.Second))
		}
	}
	return 0
}

// observe records the engine-level signals of a snapshot and returns the
// health for that interval.
func (m *monitor) observe(schedLag time.Duration, dropped uint64) wire.Health {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastLag, m.lastDrops = schedLag, dropped
	if schedLag > m.th.SchedLagP99 {
		m.lagStreak++
	} else {
		m.lagStreak = 0
	}
	return evaluate(m.th, m.last, m.cpuHigh, schedLag, m.lagStreak, dropped)
}

// current returns the latest health for heartbeats.
func (m *monitor) current() wire.Health {
	m.mu.Lock()
	defer m.mu.Unlock()
	return evaluate(m.th, m.last, m.cpuHigh, m.lastLag, m.lagStreak, m.lastDrops)
}

// idle clears the engine-level signals when no run is active, so an old
// run's lag does not keep a worker marked saturated.
func (m *monitor) idle() {
	m.mu.Lock()
	m.lastLag, m.lastDrops, m.lagStreak = 0, 0, 0
	m.mu.Unlock()
}
