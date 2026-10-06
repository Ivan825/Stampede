# Reading a report

Every run produces the same report, whether it ran with `stampede run`, on
the server, or across workers. Export it as HTML (self-contained, no network
requests, light and dark), JSON, JUnit XML or Markdown.

## Verdict

| Verdict | Meaning |
|---|---|
| **pass** | every target held (for breakpoint runs: at least one level held) |
| **fail** | at least one target missed |
| **generator-limited** | targets failed while a worker was saturated, so the failure may be the generator's |
| **no targets** | the scenario sets no targets |

`stampede run` and `stampede start` exit with 3 on fail; 0 otherwise.

## Sections

- **Targets**: each target, the observed value and pass or fail. A target
  with no data fails.
- **Breakpoint** and **knee**: see [test types](test-types.md).
- **Summary**: requests and rate, failed requests, p50/p95/p99, iterations,
  dropped iterations, peak users, bytes.
- **Over time**: throughput with planned load and active users, latency
  p50/p95/p99, error rate.
- **Journeys and steps**: per step requests, errors, percentiles, mean wait
  (time to first byte) and connect time; for streams, time to first event and
  events per second; the HTTP protocol versions used.
- **Target metrics**: with `observe.prometheus`, one chart per PromQL query
  over the run, with its minimum, maximum and last value
  ([integrations](integrations.md)).
- **Slowest requests**: the five slowest requests of each step with their
  latency, when they were sent, their status and the W3C trace ID they
  carried; a link when `observe.traces` gives a link template.
- **Errors**: each kind of failure at each step with counts, for example
  `HTTP 503`, `timeout`, `connection refused`, `check status (got 302)`,
  `extract token`.
- **Workers** (distributed runs): each worker's share, requests, peak users,
  clock offset, saturated windows and whether it was lost.
- **Notes**: anything that affects how to read the numbers: dropped
  iterations, lost workers, saturation, an early stop.

## Latency and service time

The summary's latency is measured from each request's scheduled time. When
it is noticeably higher than service time (shown just below it), requests
waited before they were sent, usually because all users were busy. See
[measurement](../concepts/measurement.md).
