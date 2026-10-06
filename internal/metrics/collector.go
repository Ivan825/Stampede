package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// Collector receives samples from many virtual users concurrently and
// produces one Snapshot per interval. Users are spread over lock-striped
// shards so recording does not contend on a single mutex.
type Collector struct {
	shards  []shard
	dropped atomic.Uint64
	seq     uint64
	flushMu sync.Mutex
}

type shard struct {
	mu   sync.Mutex
	cur  *Snapshot
	runP map[int]*[NumPhases]*Histogram
	_    [40]byte // keep shards on separate cache lines
}

// NewCollector creates a collector with n shards (at least 1).
func NewCollector(n int) *Collector {
	if n < 1 {
		n = 1
	}
	c := &Collector{shards: make([]shard, n)}
	for i := range c.shards {
		c.shards[i].cur = NewSnapshot(0)
		c.shards[i].runP = map[int]*[NumPhases]*Histogram{}
	}
	return c
}

func (c *Collector) shard(vu int) *shard {
	if vu < 0 {
		vu = -vu
	}
	return &c.shards[vu%len(c.shards)]
}

// Record adds a completed request from virtual user vu.
func (c *Collector) Record(vu int, s *Sample) {
	sh := c.shard(vu)
	sh.mu.Lock()
	sh.cur.Step(s.Step).Add(s)
	ph := sh.runP[s.Step]
	if ph == nil {
		ph = &[NumPhases]*Histogram{}
		for i := range ph {
			ph[i] = NewHistogram()
		}
		sh.runP[s.Step] = ph
	}
	for i, d := range s.Phases {
		if d > 0 {
			ph[i].RecordDuration(d)
		}
	}
	sh.mu.Unlock()
}

// IterationStarted counts a journey iteration starting, along with how
// late it was dispatched compared with its schedule.
func (c *Collector) IterationStarted(vu, journey int, lag time.Duration) {
	sh := c.shard(vu)
	sh.mu.Lock()
	sh.cur.Journey(journey).Started++
	sh.cur.SchedLag.RecordDuration(lag)
	sh.mu.Unlock()
}

// IterationDone counts a finished iteration.
func (c *Collector) IterationDone(vu, journey int, d time.Duration, ok bool) {
	sh := c.shard(vu)
	sh.mu.Lock()
	j := sh.cur.Journey(journey)
	if ok {
		j.Completed++
	} else {
		j.Failed++
	}
	j.Duration.RecordDuration(d)
	sh.mu.Unlock()
}

// Dropped counts iterations that could not start because no user was free.
func (c *Collector) Dropped(n uint64) { c.dropped.Add(n) }

// Flush closes the current interval and returns its snapshot. vus and
// planned are gauges sampled by the caller at flush time.
func (c *Collector) Flush(interval int64, vus int, planned float64) *Snapshot {
	c.flushMu.Lock()
	defer c.flushMu.Unlock()
	out := NewSnapshot(interval)
	for i := range c.shards {
		sh := &c.shards[i]
		next := NewSnapshot(interval + 1)
		sh.mu.Lock()
		cur := sh.cur
		sh.cur = next
		sh.mu.Unlock()
		out.Merge(cur)
	}
	out.Dropped = c.dropped.Swap(0)
	out.VUs = vus
	out.Planned = planned
	c.seq++
	out.Seq = c.seq
	return out
}

// PhaseHistograms returns run-wide per-phase histograms for every step.
func (c *Collector) PhaseHistograms() map[int]*[NumPhases]*Histogram {
	out := map[int]*[NumPhases]*Histogram{}
	for i := range c.shards {
		sh := &c.shards[i]
		sh.mu.Lock()
		for id, ph := range sh.runP {
			dst := out[id]
			if dst == nil {
				dst = &[NumPhases]*Histogram{}
				for j := range dst {
					dst[j] = NewHistogram()
				}
				out[id] = dst
			}
			for j := range ph {
				dst[j].Merge(ph[j])
			}
		}
		sh.mu.Unlock()
	}
	return out
}
