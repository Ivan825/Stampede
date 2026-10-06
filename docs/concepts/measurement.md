# How Stampede measures

Every other feature depends on these numbers, so they are built to be
right and tested against ground truth.

## Latency from the intended send time

In the open model each iteration has an intended start time. Latency is
measured from that time, not from when the request actually left. If the
generator, or a queue in front of the system, delayed a request, that delay
is part of what a user would have experienced, so it is counted. Leaving it
out is called **coordinated omission** and makes overloaded systems look
fast.

Reports show both: **latency** (from the intended time) and **service
time** (from the actual send). When they differ, the gap is queueing.

## Phases

Every HTTP request records DNS, connect, TLS, wait (time to first byte) and
download times. Streams also record time to first event.

## Histograms, never averaged percentiles

Each worker records every latency into histograms with the same bucketing as
HdrHistogram at three significant digits: values under 2 ms are exact and
larger ones are within 0.1%. There is one histogram per step per second.
Workers send the histograms themselves, and the server merges them without
loss, so a p99 across ten workers is the true p99, not an average of ten
p99s. The knee curve and comparisons also work on merged histograms.

## Accuracy

The `bench/` harness drives a server whose response times follow a known
distribution and compares Stampede's p50, p95 and p99 with the exact values
the server recorded. The rule is: within 2% or 1 ms, whichever is larger. A
quick version gates every push in CI; the full version runs nightly. See
[bench/README.md](../../bench/README.md).

## Saturation

An overloaded load generator inflates latency and blames the system under
test. Workers watch themselves every second:

| Signal | Saturated when |
|---|---|
| CPU | above 85% for 5 seconds |
| Scheduling lag p99 | above 10 ms for two intervals in a row |
| Dropped iterations | any |
| Go GC pause p99 | above 5 ms |
| Open file descriptors | above 80% of the limit |

Saturated intervals are listed in the report for each worker. If a target
fails while a worker was saturated, the verdict is **generator-limited**
instead of fail.

## DNS

New connections resolve host names through a cache shared by all users of a
worker (30 seconds by default, `target.http.dnsCacheTTL`), the way an
operating system's cache does. Without it, ramp-ups can stall on lookups
and understate the system's capacity.
