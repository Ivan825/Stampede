package main

import "testing"

// Trimmed wrk2 --latency output: the summary has no 95% line, so p95 comes
// from the spectrum (values in ms).
const wrk2Output = `Running 20s test @ http://127.0.0.1:9099/echo
  2 threads and 20 connections
  Thread calibration: mean lat.: 10.412ms, rate sampling interval: 20ms
  Thread calibration: mean lat.: 10.398ms, rate sampling interval: 20ms
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency    10.31ms  301.22us  11.38ms   71.20%
    Req/Sec   105.02     21.30   150.00     79.81%
  Latency Distribution (HdrHistogram - Recorded Latency)
 50.000%   10.24ms
 75.000%   10.53ms
 90.000%   10.85ms
 99.000%   11.31ms
 99.900%   11.37ms
 99.990%   11.38ms
 99.999%   11.38ms
100.000%   11.38ms

  Detailed Percentile spectrum:
       Value   Percentile   TotalCount 1/(1-Percentile)

       9.704     0.000000            1         1.00
      10.239     0.500000         1998         2.00
      10.847     0.900000         3592        10.00
      10.951     0.937500         3743        16.00
      11.007     0.950000         3793        20.00
      11.071     0.956250         3818        22.86
      11.311     0.990625         3955       106.67
      11.383     1.000000         3992          inf
#[Mean    =       10.312, StdDev   =        0.301]
#[Max     =       11.376, Total count    =         3992]
#[Buckets =           27, SubBuckets     =         2048]
----------------------------------------------------------
  3994 requests in 20.00s, 0.98MB read
Requests/sec:    199.69
Transfer/sec:     50.12KB
`

func TestParseWrk2(t *testing.T) {
	p, ok := parseWrk2([]byte(wrk2Output))
	if !ok {
		t.Fatal("parseWrk2 did not find p50, p95 and p99")
	}
	if p.P50 != 10.24 || p.P95 != 11.007 || p.P99 != 11.31 {
		t.Fatalf("got p50 %v p95 %v p99 %v, want 10.24 11.007 11.31", p.P50, p.P95, p.P99)
	}
}

func TestParseWrk2Units(t *testing.T) {
	out := " 50.000%  950.00us\n 99.000%    1.20s\n       0.950     0.950000   10   20.00\n"
	p, ok := parseWrk2([]byte(out))
	if !ok || p.P50 != 0.95 || p.P95 != 0.95 || p.P99 != 1200 {
		t.Fatalf("got %+v ok=%v", p, ok)
	}
}

// Plain wrk prints a different distribution and no spectrum.
func TestParseWrk2RejectsWrk(t *testing.T) {
	out := "  Latency Distribution\n     50%   10.24ms\n     75%   10.53ms\n     90%   10.85ms\n     99%   11.31ms\n"
	if _, ok := parseWrk2([]byte(out)); ok {
		t.Fatal("plain wrk output was accepted")
	}
}
