// Package metrics records latencies and counters during a run and merges
// them losslessly across intervals and workers.
package metrics

import (
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
	"sort"
	"time"
)

// Histogram is a log-linear latency histogram with the same bucketing as
// HdrHistogram at 3 significant digits: values below 2048 µs are exact and
// larger values are kept within 0.1% relative error. Storage is chunked
// and allocated on demand, so a histogram covering a narrow latency band
// costs a few KB instead of HdrHistogram's ~140 KB dense array. That matters
// because a worker keeps one per step per second.
//
// Values are recorded in microseconds. Histograms are not safe for
// concurrent use.
type Histogram struct {
	chunks [numChunks]*chunk
	count  uint64
	sum    uint64 // µs
	min    uint64
	max    uint64
}

const (
	subBucketBits  = 11 // 2048 sub-buckets: 3 significant digits
	subBucketCount = 1 << subBucketBits
	subBucketHalf  = subBucketCount / 2
	chunkBits      = 10
	chunkSize      = 1 << chunkBits
	// maxValue caps recorded values at about 4.7 hours in microseconds.
	maxValue  = 1<<34 - 1
	numChunks = (subBucketCount + (34-subBucketBits)*subBucketHalf) / chunkSize
)

type chunk [chunkSize]uint32

// NewHistogram returns an empty histogram.
func NewHistogram() *Histogram { return &Histogram{} }

// bucketIndex maps a value to its bucket.
func bucketIndex(v uint64) int {
	if v < subBucketCount {
		return int(v)
	}
	shift := bits.Len64(v) - subBucketBits
	sub := v >> uint(shift) // in [1024, 2048)
	return subBucketCount + (shift-1)*subBucketHalf + int(sub-subBucketHalf)
}

// bucketRange returns the lowest and highest values that map to index i.
func bucketRange(i int) (lo, hi uint64) {
	if i < subBucketCount {
		return uint64(i), uint64(i)
	}
	j := i - subBucketCount
	shift := uint(j/subBucketHalf + 1)
	sub := uint64(j%subBucketHalf + subBucketHalf)
	lo = sub << shift
	return lo, lo + (1 << shift) - 1
}

// Record adds one value in microseconds.
func (h *Histogram) Record(us uint64) { h.RecordN(us, 1) }

// RecordDuration adds one duration, rounded to microseconds.
func (h *Histogram) RecordDuration(d time.Duration) {
	if d < 0 {
		d = 0
	}
	h.RecordN(uint64((d+500)/time.Microsecond), 1)
}

// RecordN adds n occurrences of a value in microseconds.
func (h *Histogram) RecordN(us uint64, n uint64) {
	if n == 0 {
		return
	}
	if us > maxValue {
		us = maxValue
	}
	idx := bucketIndex(us)
	c := h.chunks[idx>>chunkBits]
	if c == nil {
		c = new(chunk)
		h.chunks[idx>>chunkBits] = c
	}
	slot := &c[idx&(chunkSize-1)]
	// Counts per bucket are uint32; spill into repeated adds for huge n.
	for n > 0 {
		add := n
		if room := uint64(math.MaxUint32 - *slot); add > room {
			add = room
		}
		if add == 0 {
			// Saturated bucket (over 4 billion values): drop the excess.
			break
		}
		*slot += uint32(add)
		n -= add
		if h.count == 0 || us < h.min {
			h.min = us
		}
		if us > h.max {
			h.max = us
		}
		h.count += add
		h.sum += us * add
	}
}

// Count is the number of recorded values.
func (h *Histogram) Count() uint64 { return h.count }

// Min is the smallest recorded value in µs (0 when empty).
func (h *Histogram) Min() uint64 { return h.min }

// Max is the largest recorded value in µs (0 when empty).
func (h *Histogram) Max() uint64 { return h.max }

// Mean is the exact arithmetic mean in µs.
func (h *Histogram) Mean() float64 {
	if h.count == 0 {
		return 0
	}
	return float64(h.sum) / float64(h.count)
}

// Sum is the exact total of recorded values in µs.
func (h *Histogram) Sum() uint64 { return h.sum }

// Quantile returns the value at quantile q (0..1) in µs, reported as the
// highest value equivalent to the bucket, as HdrHistogram does. The result
// never exceeds the recorded maximum.
func (h *Histogram) Quantile(q float64) uint64 {
	if h.count == 0 {
		return 0
	}
	if q <= 0 {
		return h.min
	}
	if q >= 1 {
		return h.max
	}
	target := uint64(math.Ceil(q * float64(h.count)))
	if target == 0 {
		target = 1
	}
	var seen uint64
	for ci, c := range h.chunks {
		if c == nil {
			continue
		}
		for j, n := range c {
			if n == 0 {
				continue
			}
			seen += uint64(n)
			if seen >= target {
				_, hi := bucketRange(ci<<chunkBits | j)
				if hi > h.max {
					hi = h.max
				}
				if hi < h.min {
					hi = h.min
				}
				return hi
			}
		}
	}
	return h.max
}

// Quantiles returns several quantiles in one pass. qs must be ascending.
func (h *Histogram) Quantiles(qs ...float64) []uint64 {
	out := make([]uint64, len(qs))
	for i, q := range qs {
		out[i] = h.Quantile(q)
	}
	return out
}

// Merge adds every value from o into h.
func (h *Histogram) Merge(o *Histogram) {
	if o == nil || o.count == 0 {
		return
	}
	for ci, oc := range o.chunks {
		if oc == nil {
			continue
		}
		c := h.chunks[ci]
		if c == nil {
			c = new(chunk)
			h.chunks[ci] = c
		}
		for j, n := range oc {
			if n != 0 {
				s := uint64(c[j]) + uint64(n)
				if s > math.MaxUint32 {
					s = math.MaxUint32
				}
				c[j] = uint32(s)
			}
		}
	}
	if h.count == 0 || o.min < h.min {
		h.min = o.min
	}
	if o.max > h.max {
		h.max = o.max
	}
	h.count += o.count
	h.sum += o.sum
}

// Reset empties the histogram, keeping allocated chunks for reuse.
func (h *Histogram) Reset() {
	for _, c := range h.chunks {
		if c != nil {
			*c = chunk{}
		}
	}
	h.count, h.sum, h.min, h.max = 0, 0, 0, 0
}

// Clone returns a deep copy.
func (h *Histogram) Clone() *Histogram {
	c := &Histogram{count: h.count, sum: h.sum, min: h.min, max: h.max}
	for i, ch := range h.chunks {
		if ch != nil {
			cp := *ch
			c.chunks[i] = &cp
		}
	}
	return c
}

// Bucket is one non-empty bucket, for export and charts.
type Bucket struct {
	Lo, Hi uint64 // inclusive value range in µs
	Count  uint64
}

// Buckets lists non-empty buckets in ascending order.
func (h *Histogram) Buckets() []Bucket {
	var out []Bucket
	h.each(func(idx int, n uint32) {
		lo, hi := bucketRange(idx)
		out = append(out, Bucket{Lo: lo, Hi: hi, Count: uint64(n)})
	})
	return out
}

func (h *Histogram) each(fn func(idx int, n uint32)) {
	for ci, c := range h.chunks {
		if c == nil {
			continue
		}
		for j, n := range c {
			if n != 0 {
				fn(ci<<chunkBits|j, n)
			}
		}
	}
}

// Encoding format, version 1:
//
//	byte    version (1)
//	uvarint count, sum, min, max
//	uvarint number of non-empty buckets
//	repeated: uvarint index delta from previous index, uvarint count
const encodingVersion = 1

// MarshalBinary encodes the histogram compactly (sparse, varint).
func (h *Histogram) MarshalBinary() ([]byte, error) {
	buf := make([]byte, 0, 32)
	buf = append(buf, encodingVersion)
	buf = binary.AppendUvarint(buf, h.count)
	buf = binary.AppendUvarint(buf, h.sum)
	buf = binary.AppendUvarint(buf, h.min)
	buf = binary.AppendUvarint(buf, h.max)
	var idxs []int
	var counts []uint32
	h.each(func(idx int, n uint32) {
		idxs = append(idxs, idx)
		counts = append(counts, n)
	})
	buf = binary.AppendUvarint(buf, uint64(len(idxs)))
	prev := 0
	for i, idx := range idxs {
		buf = binary.AppendUvarint(buf, uint64(idx-prev))
		buf = binary.AppendUvarint(buf, uint64(counts[i]))
		prev = idx
	}
	return buf, nil
}

var errCorrupt = errors.New("metrics: corrupt histogram encoding")

// UnmarshalBinary decodes data produced by MarshalBinary, replacing h.
func (h *Histogram) UnmarshalBinary(data []byte) error {
	*h = Histogram{}
	if len(data) == 0 {
		return nil
	}
	if data[0] != encodingVersion {
		return errCorrupt
	}
	data = data[1:]
	next := func() (uint64, error) {
		v, n := binary.Uvarint(data)
		if n <= 0 {
			return 0, errCorrupt
		}
		data = data[n:]
		return v, nil
	}
	var hdr [5]uint64
	for i := range hdr {
		v, err := next()
		if err != nil {
			return err
		}
		hdr[i] = v
	}
	h.count, h.sum, h.min, h.max = hdr[0], hdr[1], hdr[2], hdr[3]
	idx := 0
	var total uint64
	for i := uint64(0); i < hdr[4]; i++ {
		d, err := next()
		if err != nil {
			return err
		}
		n, err := next()
		if err != nil {
			return err
		}
		idx += int(d)
		if idx < 0 || idx>>chunkBits >= numChunks || n > math.MaxUint32 {
			return errCorrupt
		}
		c := h.chunks[idx>>chunkBits]
		if c == nil {
			c = new(chunk)
			h.chunks[idx>>chunkBits] = c
		}
		c[idx&(chunkSize-1)] = uint32(n)
		total += n
	}
	if total > h.count {
		return errCorrupt
	}
	return nil
}

// Percentiles is a fixed summary of a histogram, in seconds.
type Percentiles struct {
	Count uint64  `json:"count"`
	Min   float64 `json:"min"`
	Mean  float64 `json:"mean"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	P999  float64 `json:"p999"`
	Max   float64 `json:"max"`
}

// Summary computes the standard percentile set in seconds.
func (h *Histogram) Summary() Percentiles {
	if h == nil || h.count == 0 {
		return Percentiles{}
	}
	q := h.Quantiles(0.5, 0.9, 0.95, 0.99, 0.999)
	us := func(v uint64) float64 { return float64(v) / 1e6 }
	return Percentiles{
		Count: h.count, Min: us(h.min), Mean: h.Mean() / 1e6,
		P50: us(q[0]), P90: us(q[1]), P95: us(q[2]), P99: us(q[3]), P999: us(q[4]),
		Max: us(h.max),
	}
}

// QuantileSeconds is Quantile converted to seconds.
func (h *Histogram) QuantileSeconds(q float64) float64 {
	return float64(h.Quantile(q)) / 1e6
}

// sortedKeys is a small helper for deterministic map iteration.
func sortedKeys[V any](m map[int]V) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
