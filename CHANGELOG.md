# Changelog

Notable changes to Stampede. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions
follow [Semantic Versioning](https://semver.org/). The website's changelog
page is built from this file.

## Unreleased

Everything below is in the repository and will be in v1.0, the first
release. Features still being built are listed as planned in
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
- Product packs for eleven kinds of product, detected by `stampede init`.

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
- A Helm chart.
- A Kubernetes operator (alpha).
- Terraform examples for workers in several regions.

### Safety

- Ownership verification for public targets, and low caps until it is
  verified.
- Requests confined to the target and explicitly allowed hosts.
- A kill switch in the web UI, CLI and API.
- Roles, API tokens, encrypted secrets and an audit log.
- Single sign-on with OpenID Connect.
