package worker

import (
	"strings"
	"testing"
	"time"
)

func TestEvaluateThresholds(t *testing.T) {
	calm := sysSample{cpuPercent: 40, cpuOK: true, gcPauseP99: time.Millisecond, openFDs: 100, fdLimit: 1024, goroutines: 50}
	tests := []struct {
		name     string
		s        sysSample
		cpuHigh  time.Duration
		lag      time.Duration
		dropped  uint64
		reason   string // substring expected in the only reason; "" = healthy
		saturate bool
	}{
		{name: "healthy", s: calm, lag: 2 * time.Millisecond},
		{name: "cpu high but brief", s: with(calm, func(s *sysSample) { s.cpuPercent = 95 }), cpuHigh: 3 * time.Second},
		{name: "cpu high for 5s", s: with(calm, func(s *sysSample) { s.cpuPercent = 95 }), cpuHigh: 5 * time.Second, reason: "cpu 95%", saturate: true},
		{name: "cpu unknown", s: with(calm, func(s *sysSample) { s.cpuPercent, s.cpuOK = 99, false }), cpuHigh: time.Minute},
		{name: "cpu at threshold", s: with(calm, func(s *sysSample) { s.cpuPercent = 85 }), cpuHigh: time.Minute},
		{name: "sched lag", s: calm, lag: 11 * time.Millisecond, reason: "scheduling lag", saturate: true},
		{name: "sched lag at threshold", s: calm, lag: 10 * time.Millisecond},
		{name: "dropped", s: calm, dropped: 1, reason: "1 iterations dropped", saturate: true},
		{name: "gc pause", s: with(calm, func(s *sysSample) { s.gcPauseP99 = 6 * time.Millisecond }), reason: "GC pause", saturate: true},
		{name: "fds near limit", s: with(calm, func(s *sysSample) { s.openFDs = 900 }), reason: "900 of 1024 file descriptors", saturate: true},
		{name: "fds at 80%", s: with(calm, func(s *sysSample) { s.openFDs, s.fdLimit = 800, 1000 })},
		{name: "fd limit unknown", s: with(calm, func(s *sysSample) { s.openFDs, s.fdLimit = 5000, 0 })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := evaluate(DefaultThresholds, tt.s, tt.cpuHigh, tt.lag, lagIntervals, tt.dropped)
			if h.Saturated != tt.saturate {
				t.Fatalf("saturated=%v reasons=%v, want %v", h.Saturated, h.Reasons, tt.saturate)
			}
			if tt.reason == "" {
				if len(h.Reasons) != 0 {
					t.Errorf("unexpected reasons %v", h.Reasons)
				}
				return
			}
			if len(h.Reasons) != 1 || !strings.Contains(h.Reasons[0], tt.reason) {
				t.Errorf("reasons %v, want one containing %q", h.Reasons, tt.reason)
			}
		})
	}
}

func TestEvaluateReportsEveryReason(t *testing.T) {
	s := sysSample{cpuPercent: 99, cpuOK: true, gcPauseP99: 9 * time.Millisecond, openFDs: 99, fdLimit: 100}
	h := evaluate(DefaultThresholds, s, 6*time.Second, 50*time.Millisecond, lagIntervals, 7)
	if !h.Saturated || len(h.Reasons) != 5 {
		t.Errorf("reasons %v, want all five", h.Reasons)
	}
	if h.Dropped != 7 || h.SchedLagP99 != 50*time.Millisecond || h.CPUPercent != 99 {
		t.Errorf("figures not carried: %+v", h)
	}
}

func TestMonitorSamplesProcess(t *testing.T) {
	m := newMonitor(DefaultThresholds)
	// Burn a little CPU and allocate so the sample has something to see.
	deadline := time.Now().Add(50 * time.Millisecond)
	var sink []byte
	for time.Now().Before(deadline) {
		sink = make([]byte, 1<<16)
	}
	_ = sink
	m.sample()
	h := m.current()
	if h.Goroutines <= 0 {
		t.Errorf("goroutines = %d", h.Goroutines)
	}
	if _, ok := processCPU(); ok && h.CPUPercent <= 0 {
		t.Errorf("cpu = %v after busy loop", h.CPUPercent)
	}
	if _, limit, ok := fdUsage(); ok && (h.FDLimit != limit || h.OpenFDs == 0) {
		t.Errorf("fds %d of %d", h.OpenFDs, h.FDLimit)
	}
	if got := m.observe(20*time.Millisecond, 0); got.Saturated {
		t.Errorf("one interval of 20ms lag should not saturate yet: %+v", got)
	}
	if got := m.observe(20*time.Millisecond, 0); !got.Saturated {
		t.Errorf("20ms scheduling lag for two intervals should saturate: %+v", got)
	}
	m.idle()
	if got := m.current(); got.SchedLagP99 != 0 {
		t.Errorf("idle kept lag %v", got.SchedLagP99)
	}
}

func with(s sysSample, f func(*sysSample)) sysSample {
	f(&s)
	return s
}

func TestSingleLagIntervalIsNotSaturation(t *testing.T) {
	h := evaluate(DefaultThresholds, sysSample{}, 0, 50*time.Millisecond, 1, 0)
	if h.Saturated {
		t.Errorf("one interval of lag should not count: %v", h.Reasons)
	}
	h = evaluate(DefaultThresholds, sysSample{}, 0, 50*time.Millisecond, 2, 0)
	if !h.Saturated {
		t.Error("sustained lag should count")
	}
}
