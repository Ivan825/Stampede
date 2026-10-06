package report

import (
	"bytes"
	"strings"
	"testing"
)

func fake(p95, rps float64) *Report {
	r := &Report{Scenario: "s", Duration: 60, Load: LoadInfo{Executor: "constant-rate", Mode: "rate", Peak: 100, Workers: 2}}
	r.Overall.Requests = 1000
	r.Overall.Latency.P50 = p95 / 2
	r.Overall.Latency.P95 = p95
	r.Overall.Latency.P99 = p95 * 1.5
	r.Overall.RPS = rps
	return r
}

func find(c *Comparison, name string) MetricDelta {
	for _, m := range c.Metrics {
		if m.Name == name {
			return m
		}
	}
	return MetricDelta{}
}

func TestCompareRegression(t *testing.T) {
	a := []*Report{fake(0.100, 100), fake(0.102, 100), fake(0.099, 100)}
	b := []*Report{fake(0.150, 100), fake(0.148, 100), fake(0.152, 100)}
	c := Compare(a, b, "v1", "v2")
	if c.Verdict != CmpRegression || !c.Comparable {
		t.Fatalf("verdict %s %v", c.Verdict, c.Problems)
	}
	p95 := find(c, "p95")
	if p95.Verdict != CmpRegression || p95.CILow <= 0 {
		t.Errorf("p95 %+v", p95)
	}
	if find(c, "throughput").Verdict != CmpNoChange {
		t.Errorf("throughput should be unchanged")
	}
}

func TestCompareNoiseIsNotARegression(t *testing.T) {
	// Repeats of each version vary by ±10%; a 5% shift is inside the noise.
	a := []*Report{fake(0.090, 100), fake(0.110, 100), fake(0.100, 100)}
	b := []*Report{fake(0.095, 100), fake(0.115, 100), fake(0.105, 100)}
	c := Compare(a, b, "v1", "v2")
	if v := find(c, "p95").Verdict; v != CmpNoChange {
		t.Errorf("p95 verdict %s, want no-change", v)
	}
}

func TestCompareSingleRunsAreInconclusive(t *testing.T) {
	c := Compare([]*Report{fake(0.1, 100)}, []*Report{fake(0.2, 100)}, "a", "b")
	if c.Verdict != CmpInconclusive {
		t.Errorf("verdict %s", c.Verdict)
	}
}

func TestCompareImprovementAndNotComparable(t *testing.T) {
	a := []*Report{fake(0.2, 100), fake(0.2, 101), fake(0.21, 99)}
	b := []*Report{fake(0.1, 100), fake(0.1, 100), fake(0.11, 101)}
	if c := Compare(a, b, "a", "b"); c.Verdict != CmpImprovement {
		t.Errorf("verdict %s", c.Verdict)
	}
	other := fake(0.1, 100)
	other.Load.Workers = 5
	c := Compare(a, []*Report{other, fake(0.1, 100), fake(0.1, 100)}, "a", "b")
	if c.Comparable || c.Verdict != CmpInconclusive {
		t.Errorf("worker mismatch should be not comparable: %v %s", c.Problems, c.Verdict)
	}
	var buf bytes.Buffer
	c.WriteMarkdown(&buf)
	if !strings.Contains(buf.String(), "Not comparable") {
		t.Error(buf.String())
	}
}

func TestCompareIgnoresSubMillisecondShifts(t *testing.T) {
	a := []*Report{fake(0.0020, 100), fake(0.0020, 100), fake(0.0020, 100)}
	b := []*Report{fake(0.0026, 100), fake(0.0026, 100), fake(0.0026, 100)} // +30% but only 0.6ms
	if v := find(Compare(a, b, "a", "b"), "p95").Verdict; v != CmpNoChange {
		t.Errorf("p95 verdict %s", v)
	}
}
