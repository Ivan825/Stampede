package coordinator

import (
	"sort"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
)

// merger combines per-worker snapshots into one snapshot per interval.
//
// Each snapshot is keyed by (worker, seq): a resend after a reconnect is
// recognised and never counted twice. Interval k is emitted, in order,
// once every worker still expected to report it has done so, or once it
// is grace late, whichever is first; workers that had not reported are
// listed as missing. Data that arrives after its interval was emitted is
// still merged into the stored interval, so the final result is complete
// even when the live stream was not.
type merger struct {
	n       int
	t0      time.Time
	iv      time.Duration
	grace   time.Duration
	planned func(k int64) float64

	seen    []map[uint64]bool
	pending map[int64]*intervalAcc
	stored  map[int64]*metrics.Snapshot
	next    int64
	maxSeen int64
	late    uint64
}

type intervalAcc struct {
	snap     *metrics.Snapshot
	reported []bool
}

// emitted is a merged interval ready for the live stream.
type emitted struct {
	snap    *metrics.Snapshot
	missing []int // member indexes that had not reported
}

func newMerger(n int, t0 time.Time, iv, grace time.Duration, planned func(int64) float64) *merger {
	m := &merger{
		n: n, t0: t0, iv: iv, grace: grace, planned: planned,
		seen: make([]map[uint64]bool, n), pending: map[int64]*intervalAcc{},
		stored: map[int64]*metrics.Snapshot{}, maxSeen: -1,
	}
	for i := range m.seen {
		m.seen[i] = map[uint64]bool{}
	}
	return m
}

// grow adds a member (a spare taking over a lost share).
func (m *merger) grow() {
	m.n++
	m.seen = append(m.seen, map[uint64]bool{})
	for _, acc := range m.pending {
		acc.reported = append(acc.reported, false)
	}
}

// add merges a snapshot from member. dup reports a resend that was
// ignored; late reports data for an interval already emitted.
func (m *merger) add(member int, s *metrics.Snapshot) (dup, late bool) {
	if m.seen[member][s.Seq] {
		return true, false
	}
	m.seen[member][s.Seq] = true
	k := s.Interval
	if k > m.maxSeen {
		m.maxSeen = k
	}
	if k < m.next {
		// Emitted already: keep the data for the final result.
		st := m.stored[k]
		if st == nil {
			st = metrics.NewSnapshot(k)
			m.stored[k] = st
		}
		st.Merge(s)
		m.late++
		return false, true
	}
	acc := m.pending[k]
	if acc == nil {
		acc = &intervalAcc{snap: metrics.NewSnapshot(k), reported: make([]bool, m.n)}
		m.pending[k] = acc
	}
	acc.snap.Merge(s)
	acc.reported[member] = true
	return false, false
}

// ready returns the intervals that can be emitted now, in order.
// expected reports whether a member should still report interval k. With
// force, every interval up to the latest seen is emitted regardless.
func (m *merger) ready(now time.Time, expected func(member int, k int64) bool, force bool) []emitted {
	var out []emitted
	for m.next <= m.maxSeen {
		k := m.next
		acc := m.pending[k]
		var missing []int
		for i := 0; i < m.n; i++ {
			if expected(i, k) && (acc == nil || !acc.reported[i]) {
				missing = append(missing, i)
			}
		}
		deadline := m.t0.Add(time.Duration(k+1)*m.iv + m.grace)
		if len(missing) > 0 && !force && now.Before(deadline) {
			break
		}
		st := metrics.NewSnapshot(k)
		if acc != nil {
			st = acc.snap
		}
		st.Planned = m.planned(k)
		st.Seq = uint64(k) + 1
		m.stored[k] = st
		delete(m.pending, k)
		m.next++

		// The stream gets a copy: late data keeps merging into st.
		cp := metrics.NewSnapshot(k)
		cp.Merge(st)
		cp.Planned, cp.Seq = st.Planned, st.Seq
		out = append(out, emitted{snap: cp, missing: missing})
	}
	return out
}

// all returns every stored interval, including late data, in order.
func (m *merger) all() []*metrics.Snapshot {
	ks := make([]int64, 0, len(m.stored))
	for k := range m.stored {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	out := make([]*metrics.Snapshot, len(ks))
	for i, k := range ks {
		st := m.stored[k]
		st.Planned = m.planned(k)
		st.Seq = uint64(k) + 1
		out[i] = st
	}
	return out
}
