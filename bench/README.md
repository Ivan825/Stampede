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
| Side-by-side with k6 on the same workload | Planned |
| Side-by-side with wrk2 on the same workload | Planned |
