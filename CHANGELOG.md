# Changelog

Notable changes to Stampede. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions
follow [Semantic Versioning](https://semver.org/). The website's changelog
page is built from this file.

## 1.0.0 (2026-10-07)

The first release. Features still being built are listed as planned in
[features.yaml](features.yaml) and are not included here.

### Measurement

- Latency measured from each request's scheduled send time, so queueing is
  not hidden (coordinated omission).
- Percentiles from merged HDR-style histograms, never averaged.
- DNS, connect, TLS, wait and download timing for every request.
- Load-generator saturation detection, and a generator-limited verdict
  when a saturated worker may have caused a failure.
- An accuracy check against a calibrated server in CI.

### Load

- Open (arrival rate) and closed (virtual users) load models.
- Traffic shapes: smoke, baseline, stress, spike, soak, breakpoint, steps,
  recovery and wave.
- Breakpoint search and the knee of the throughput curve.
- Network emulation per user: latency, jitter, bandwidth and packet loss.
- Auto-abort on sustained errors or latency.
- Replay of recorded traffic from access logs and HAR files.
- A fault injection agent that slows, cuts or kills dependencies mid-run.
- Test data from CSV, JSON, lists, ranges, generated fake data or a SQL
  query.
- Large-payload tests with generated bodies up to 64 MB.

### Protocols

- HTTP/1.1 and HTTP/2 (TLS and h2c).
- GraphQL with persisted queries.
- WebSocket.
- Server-sent events with time to first event.
- gRPC unary and server streaming.
- Browser steps in headless Chrome, with Web Vitals.
- Plugins for MQTT, Kafka, Redis, SQL and UDP, and an SDK with a
  conformance suite for writing others.

### Scale

- Distributed workers over gRPC with a synchronised start and a lossless
  merge of their results.
- Worker loss marked in reports, and a dead man's switch on every worker.
- An idle worker takes over a lost worker's share mid-run.
- Server replicas, standby or all active.
- Mutual TLS between server and workers with a built-in certificate
  authority.
- A run's load split across worker regions by percentage (`load.regions`,
  `--region`, or the New Run dialog), refused when a region has no
  connected worker.
- 10-second and 1-minute rollups of run metrics (continuous aggregates on
  TimescaleDB) and `--metrics-retention` for per-second metrics.

### Workflow

- A web UI with a scenario editor, live runs and reports.
- A terminal console with plain-language commands.
- HTML, PDF, CSV, JSON, JUnit and Markdown reports, and CI exit codes.
- Release comparison with confidence intervals, in the CLI and side by
  side in the web UI.
- Optional AI journey generation from specs, recordings, logs or a browser
  crawl, with every journey dry-run before it is written.
- Optional AI report narratives that cite every figure.
- A coverage map and drift detection for saved scenarios.
- Scheduled runs.
- Scheduled drift checks: a dry run of every journey and an OpenAPI diff
  on a cron, `drift.detected` notifications, and an AI repair proposal
  that a person approves, in the CLI, the API and the web UI.
- AI generation of gRPC journeys from `.proto` files, dry-run against the
  target.
- Product packs for eleven kinds of product, detected by `stampede init`.
- `stampede pack create` and `stampede plugin create` scaffold a new pack
  folder and a plugin module with a conformance test.
- `stampede validate --dry-run` runs each journey once with one user
  against the target.
- `stampede run --cluster` runs a scenario file on the server's workers
  with `run`'s flags and outputs; `stampede report` and `stampede compare`
  take server run ids as well as files.
- In the terminal console, `/init <url>`, `/run soak 4h` and
  `/run replay <file>`.
- `stampede gen` is short for `stampede generate`.

### Integrations

- Target metrics from Prometheus charted in the report.
- The slowest requests per step with links to their traces (Jaeger, Tempo).
- Notifications: signed webhooks, Slack and Discord.
- Stampede's own Prometheus metrics, OpenTelemetry traces and a Grafana
  dashboard.

### Operate

- A Docker Compose stack with the ShopLab demo app, and `stampede up`
  without a clone.
- One-line installers for Linux, macOS and Windows.
- `stampede backup` and `stampede restore`, around `pg_dump` and
  `pg_restore`.
- A Helm chart.
- A Kubernetes operator (alpha).
- Terraform examples for workers in several regions.

### Safety

- Ownership verification for public targets, and low caps until it is
  verified.
- Requests confined to the target and explicitly allowed hosts.
- A kill switch in the web UI, CLI and API.
- Roles, API tokens, encrypted secrets and an audit log.
- Per-project role overrides, set in the API or a project's settings in the
  web UI.
- Organisation and project caps on rate, virtual users and duration, edited
  in the API or the web UI, which shows each target's effective caps.
- An optional project setting that requires a passing dry run before any
  load; the run page shows each journey's dry-run result and the run's
  events.
- Runs that would call payment, SMS, email or CAPTCHA services are refused
  by the server and reported by `stampede validate`.
- Single sign-on with OpenID Connect.
