# Accuracy benchmark

Stampede's reports are only useful if the numbers are right, so accuracy is
measured, gated in CI and published.

## Method

`bench/echoserver` is a calibrated target. Every response is delayed by a
value drawn from a known distribution (fixed, uniform, lognormal or
bimodal), and the server records exactly how long it held each request,
including flushing the response. Those hold times are the ground truth.

`bench/accuracy` starts the echo server as a separate process, drives it
with Stampede's engine at a fixed arrival rate, and compares Stampede's
reported p50, p95 and p99 with the server's exact percentiles.

A percentile passes when

    |stampede - truth| <= max(2% of truth, 1 ms)

The ground truth starts when the server's handler runs, so it does not
include the kernel, connection handling or the server's own scheduling. That
work is real latency a user would see, but it counts against Stampede in
this comparison, which makes the test conservative.

A case also fails if any iteration was dropped or if the number of requests
Stampede counted differs from the number the server saw.

## Running it

```sh
go run ./bench/accuracy              # all cases, about 2 minutes
go run ./bench/accuracy -quick       # 5-second cases
go run ./bench/accuracy -only lognormal
go run ./bench/accuracy -out results.json
```

Run it on an otherwise idle machine. Other heavy processes on the same host
show up as tail latency in both the target and the generator. The nightly CI
job runs it on a dedicated Linux runner; results for each release are
published with the release notes.

## Status

| Comparison | Status |
|---|---|
| Ground truth from the calibrated echo server | Shipped |
| Side-by-side with k6 on the same workload | Shipped (`bench/compare`, nightly) |
| Side-by-side with wrk2 on the same workload | Shipped (`bench/compare`, nightly; wrk2 is built from source on Linux) |

## Comparing with k6 and wrk2

```sh
go run ./bench/compare                      # uses k6 and wrk2 from PATH when installed
go run ./bench/compare -quick -out compare.json
go run ./bench/compare -k6 ~/bin/k6 -wrk2 ~/src/wrk2/wrk
```

Each case runs the same open-model workload (a constant arrival rate) through
each tool in turn against one echo server. The server's exact percentiles are
read and reset after every tool, so each tool is judged against the ground
truth of its own run. The table says where each tool starts its clock:

- **scheduled**: Stampede's latency and wrk2 count from when the request
  should have been sent, so a generator that falls behind cannot hide it
  (coordinated omission corrected).
- **sent**: Stampede's service time and k6's `http_req_duration` count from
  when the request actually left.

On an idle generator both agree with the truth; the scheduled clock is the
one that stays honest when the generator is overloaded. A tool that is not
installed is reported as such, never guessed. The nightly CI job installs
k6, builds wrk2 and uploads the JSON results.

Example (Apple M-series laptop, busy with other work, 5-second case):

```
lognormal median 20ms  (300/s for 5s)
                             clock from    p50 ms    p95 ms    p99 ms
✓  stampede                   scheduled      21.09     52.45     76.61
     truth for that run                      20.77     52.20     75.61
✓  k6                         sent           20.53     52.96     80.10
     truth for that run                      20.21     52.81     79.92
```

## Generator throughput

`bench/scale` measures how much load one Stampede process can generate
before the generator, rather than the target, becomes the limit:

```sh
go run ./bench/scale                         # 1k to 80k requests/s, 5s per level
go run ./bench/scale -levels 5000,20000 -hold 10s -out scale.json
```

It starts the echo server as a separate process (1ms fixed delay) and runs
an open-model scenario at rising rates. A level is clean when nothing was
dropped, the achieved rate is within 2% of the plan, errors stay under
0.1% and the p99 scheduling lag stays under 10ms. It stops at the first
level that is not clean and reports the highest clean one with the CPU
count, so results from different machines can be compared. The generator
and the target share the machine, so the figure is a lower bound for a
worker on its own host.

The nightly CI job runs it on a hosted Linux runner and keeps the JSON.
Run it on an otherwise idle machine: anything else running shows up as
scheduling lag.
