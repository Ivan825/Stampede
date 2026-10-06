package metrics

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
	"testing/quick"
)

func exactQuantile(sorted []uint64, q float64) uint64 {
	idx := int(math.Ceil(q*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}

func TestBucketRoundTrip(t *testing.T) {
	for _, v := range []uint64{0, 1, 2047, 2048, 2049, 4095, 4096, 1e6, 6e7, maxValue} {
		lo, hi := bucketRange(bucketIndex(v))
		if v < lo || v > hi {
			t.Errorf("value %d not in its bucket [%d, %d]", v, lo, hi)
		}
		if v >= subBucketCount && float64(hi-lo)/float64(lo) > 0.001 {
			t.Errorf("bucket for %d too wide: [%d, %d]", v, lo, hi)
		}
	}
	// Indices are contiguous and monotonic.
	prevHi := uint64(0)
	for i := 1; i < numChunks*chunkSize; i++ {
		lo, hi := bucketRange(i)
		if lo != prevHi+1 {
			t.Fatalf("gap at index %d: lo %d after hi %d", i, lo, prevHi)
		}
		if bucketIndex(lo) != i || bucketIndex(hi) != i {
			t.Fatalf("index %d does not round trip", i)
		}
		prevHi = hi
	}
}

func TestQuantileAccuracy(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, dist := range []string{"uniform", "lognormal", "bimodal"} {
		h := NewHistogram()
		vals := make([]uint64, 100000)
		for i := range vals {
			var v float64
			switch dist {
			case "uniform":
				v = r.Float64() * 2e6
			case "lognormal":
				v = math.Exp(r.NormFloat64()*1.2 + 10)
			case "bimodal":
				if r.IntN(10) == 0 {
					v = 800000 + r.Float64()*50000
				} else {
					v = 20000 + r.Float64()*5000
				}
			}
			vals[i] = uint64(v)
			h.Record(vals[i])
		}
		slices.Sort(vals)
		for _, q := range []float64{0.5, 0.9, 0.95, 0.99, 0.999} {
			want := exactQuantile(vals, q)
			got := h.Quantile(q)
			if diff := math.Abs(float64(got)-float64(want)) / math.Max(float64(want), 1); diff > 0.001 && got-want > 1 {
				t.Errorf("%s p%v: got %d want %d (%.4f%%)", dist, q*100, got, want, diff*100)
			}
		}
		if h.Min() != vals[0] || h.Max() != vals[len(vals)-1] {
			t.Errorf("%s: min/max wrong", dist)
		}
	}
}

func TestMergeEqualsUnion(t *testing.T) {
	f := func(a, b []uint32) bool {
		ha, hb, hu := NewHistogram(), NewHistogram(), NewHistogram()
		for _, v := range a {
			ha.Record(uint64(v))
			hu.Record(uint64(v))
		}
		for _, v := range b {
			hb.Record(uint64(v))
			hu.Record(uint64(v))
		}
		ha.Merge(hb)
		if ha.Count() != hu.Count() || ha.Sum() != hu.Sum() || ha.Min() != hu.Min() || ha.Max() != hu.Max() {
			return false
		}
		for _, q := range []float64{0.1, 0.5, 0.9, 0.99, 1} {
			if ha.Quantile(q) != hu.Quantile(q) {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 300}); err != nil {
		t.Error(err)
	}
}

func TestEncodeDecode(t *testing.T) {
	f := func(vals []uint32) bool {
		h := NewHistogram()
		for _, v := range vals {
			h.Record(uint64(v))
		}
		b, err := h.MarshalBinary()
		if err != nil {
			return false
		}
		var d Histogram
		if err := d.UnmarshalBinary(b); err != nil {
			return false
		}
		if d.Count() != h.Count() || d.Sum() != h.Sum() || d.Max() != h.Max() || d.Min() != h.Min() {
			return false
		}
		return d.Quantile(0.99) == h.Quantile(0.99) && d.Quantile(0.5) == h.Quantile(0.5)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 300}); err != nil {
		t.Error(err)
	}
	var d Histogram
	if err := d.UnmarshalBinary([]byte{9, 1, 2}); err == nil {
		t.Error("expected error for bad version")
	}
	if err := d.UnmarshalBinary([]byte{1, 1}); err == nil {
		t.Error("expected error for truncated data")
	}
}

func TestEncodingIsCompact(t *testing.T) {
	h := NewHistogram()
	r := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 10000; i++ {
		h.Record(uint64(20000 + r.IntN(30000)))
	}
	b, _ := h.MarshalBinary()
	if len(b) > 12000 {
		t.Errorf("encoding is %d bytes for 10k values in a 20-50ms band", len(b))
	}
}

func TestEmptyAndReset(t *testing.T) {
	h := NewHistogram()
	if h.Quantile(0.99) != 0 || h.Mean() != 0 {
		t.Error("empty histogram should report zeros")
	}
	h.Record(500)
	h.Reset()
	if h.Count() != 0 || h.Quantile(0.5) != 0 {
		t.Error("reset did not clear")
	}
	h.Record(maxValue + 100)
	if h.Max() != maxValue {
		t.Error("value above range should clamp")
	}
}

func BenchmarkRecord(b *testing.B) {
	h := NewHistogram()
	r := rand.New(rand.NewPCG(5, 6))
	vals := make([]uint64, 4096)
	for i := range vals {
		vals[i] = uint64(math.Exp(r.NormFloat64() + 10))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Record(vals[i&4095])
	}
}
