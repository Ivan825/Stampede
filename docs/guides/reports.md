# Reading a report

Every run produces the same report, whether it ran with `stampede run`, on
the server, or across workers. Export it as HTML (self-contained, no network
requests, light and dark), PDF, CSV, JSON, JUnit XML or Markdown.

## Exports

| Format | `stampede run` / `stampede report` | Server API (`GET /api/v1/runs/{id}/report?format=`) |
|---|---|---|
| HTML | `-o report.html` | `html` |
| PDF | `--pdf report.pdf` | the web UI's **PDF** button prints the HTML report (choose "Save as PDF") |
| CSV, per step | `--csv steps.csv` | `csv` |
| CSV, per second | `--timeline-csv timeline.csv` | `timeline-csv` |
| JSON | `--json report.json` | `json` (the default) |
| JUnit XML | `--junit junit.xml` | `junit` |
| Markdown | `--md summary.md` | `markdown` |

`stampede report` takes a JSON report file or the id of a run on the
server you signed in to (a unique prefix is enough):
`stampede report 3f2a91c0 --pdf run.pdf`.

`--pdf` prints the HTML report with headless Chrome or Chromium, on A4 in
the light theme. It finds the browser as browser steps do (set
`STAMPEDE_CHROME` to choose one); the server image has no browser, which
is why the web UI prints through yours.

The per-step CSV has one row per step, then a row for its journey (empty
`step`), and a last row for the whole run (empty `journey` and `step`).
Latencies are in milliseconds, `error_rate` is a fraction, and `rps` is
requests per second. The timeline CSV has one row per second with
achieved and planned rates, error rate, p50/p95/p99, virtual users,
iterations, dropped iterations and the generator's scheduling lag.

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
