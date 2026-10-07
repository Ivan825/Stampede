// Package health watches the load generator itself: CPU, scheduling lag,
// dropped iterations, GC pauses, file descriptors, ephemeral ports and
// network throughput. A saturated generator may be what limits the
// measured load, so latencies it measured then say as much about the
// generator as about the target.
package health

import (
	"fmt"
	"math"
	"runtime"
	"runtime/metrics"
	"strings"
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
	// PortFraction of the ephemeral port range in use (Linux).
	PortFraction float64
	// NetFraction of a network interface's link speed, in either
	// direction (Linux, when the speed is known).
	NetFraction float64
	// Any dropped iteration (no virtual user free) always saturates.
}

// DefaultThresholds are the limits from the distributed-execution plan.
var DefaultThresholds = Thresholds{
	CPUPercent:   85,
	CPUFor:       5 * time.Second,
	SchedLagP99:  10 * time.Millisecond,
	GCPauseP99:   5 * time.Millisecond,
	FDFraction:   0.8,
	PortFraction: 0.8,
	NetFraction:  0.8,
}

// sysSample is one reading of the process.
type sysSample struct {
	cpuPercent float64
	cpuOK      bool
	gcPauseP99 time.Duration
	openFDs    uint64
	fdLimit    uint64
	goroutines int
	portsUsed  uint64
	portRange  uint64
	// busiest is the interface closest to its link speed, and netUse
	// that fraction.
	busiest  string
	netUse   float64
	linkBits uint64
}

// ifaceCount is one interface's byte counters and link speed (bits/s).
type ifaceCount struct {
	rx, tx, speed uint64
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
	if s.portRange > 0 && float64(s.portsUsed) > th.PortFraction*float64(s.portRange) {
		h.Reasons = append(h.Reasons, fmt.Sprintf("%d of %d ephemeral ports in use", s.portsUsed, s.portRange))
	}
	if s.linkBits > 0 && s.netUse > th.NetFraction {
		h.Reasons = append(h.Reasons, fmt.Sprintf("network %s at %.0f%% of its %d Mbit/s link", s.busiest, 100*s.netUse, s.linkBits/1_000_000))
	}
	h.Saturated = len(h.Reasons) > 0
	return h
}

// Monitor samples the process once a second. The engine-level signals
// (scheduling lag, dropped iterations) come from each snapshot.
type Monitor struct {
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
	prevNet   map[string]ifaceCount
}

const gcPauses = "/sched/pauses/total/gc:seconds"

func NewMonitor(th Thresholds) *Monitor {
	m := &Monitor{th: th, gcSample: []metrics.Sample{{Name: gcPauses}}}
	m.prevCPU, _ = processCPU()
	m.prevWall = time.Now()
	metrics.Read(m.gcSample)
	if m.gcSample[0].Value.Kind() == metrics.KindFloat64Histogram {
		m.prevGC = append([]uint64(nil), m.gcSample[0].Value.Float64Histogram().Counts...)
	}
	return m
}

// Loop samples every second until done is closed.
func (m *Monitor) Loop(done <-chan struct{}) {
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

func (m *Monitor) sample() {
	now := time.Now()
	s := sysSample{goroutines: runtime.NumGoroutine()}
	cpu, ok := processCPU()
	// The slow readings happen before taking the lock, which heartbeats
	// also need; machine-wide ones are shared by every monitor in the
	// process (a test may run hundreds of workers in one).
	s.openFDs, s.fdLimit, _ = fdUsage()
	ports := sharedPorts.get()
	s.portsUsed, s.portRange = ports.used, ports.total
	ifaces := sharedIfaces.get()

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
	m.netUse(ifaces, &s, now.Sub(m.prevWall))
	m.prevCPU, m.prevWall = cpu, now
	s.gcPauseP99 = m.gcP99()
	m.last = s
}

// cached holds a machine-wide reading for up to a second.
type cached[T any] struct {
	mu   sync.Mutex
	at   time.Time
	val  T
	read func() T
}

func (c *cached[T]) get() T {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) >= 900*time.Millisecond {
		c.val, c.at = c.read(), time.Now()
	}
	return c.val
}

type portReading struct{ used, total uint64 }

var (
	sharedPorts = &cached[portReading]{read: func() portReading {
		u, t, _ := portUsage()
		return portReading{u, t}
	}}
	sharedIfaces = &cached[map[string]ifaceCount]{read: ifaceCounters}
)

// netUse finds the interface closest to its link speed since the
// previous sample. Interfaces with an unknown speed are skipped.
func (m *Monitor) netUse(cur map[string]ifaceCount, s *sysSample, elapsed time.Duration) {
	wall := elapsed.Seconds()
	if m.prevNet != nil && wall > 0 {
		for name, c := range cur {
			p, ok := m.prevNet[name]
			if !ok || c.speed == 0 || c.rx < p.rx || c.tx < p.tx {
				continue
			}
			bits := 8 * float64(max(c.rx-p.rx, c.tx-p.tx)) / wall
			if use := bits / float64(c.speed); use > s.netUse {
				s.busiest, s.netUse, s.linkBits = name, use, c.speed
			}
		}
	}
	m.prevNet = cur
}

// gcP99 returns the p99 GC pause since the previous call, using the
// upper bound of the bucket that holds it.
func (m *Monitor) gcP99() time.Duration {
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

// Observe records the engine-level signals of a snapshot and returns the
// health for that interval.
func (m *Monitor) Observe(schedLag time.Duration, dropped uint64) wire.Health {
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

// Current returns the latest health for heartbeats.
func (m *Monitor) Current() wire.Health {
	m.mu.Lock()
	defer m.mu.Unlock()
	return evaluate(m.th, m.last, m.cpuHigh, m.lastLag, m.lagStreak, m.lastDrops)
}

// Idle clears the engine-level signals when no run is active, so an old
// run's lag does not keep a worker marked saturated.
func (m *Monitor) Idle() {
	m.mu.Lock()
	m.lastLag, m.lastDrops, m.lagStreak = 0, 0, 0
	m.mu.Unlock()
}

// Kind strips the figures from a saturation reason so a summary lists
// each kind once ("cpu", "scheduling lag", ...).
func Kind(reason string) string {
	for _, k := range []string{"cpu", "scheduling lag", "iterations dropped", "GC pause", "file descriptors", "ephemeral ports", "network"} {
		if strings.Contains(reason, k) {
			return k
		}
	}
	return reason
}
