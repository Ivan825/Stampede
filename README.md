<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="brand/logo-dark.svg">
    <img src="brand/logo-light.svg" alt="Stampede" width="300">
  </picture>
</p>

<p align="center"><b>Your launch-day traffic, before launch day.</b><br>
Describe your users. Stampede becomes a thousand of them.</p>

<p align="center">
  <a href="https://github.com/Ivan825/Stampede/actions/workflows/ci.yml"><img src="https://github.com/Ivan825/Stampede/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/licence-Apache--2.0-blue" alt="Apache-2.0"></a>
</p>

---

Stampede is an open-source, self-hosted load testing platform. You describe
how your users behave, Stampede checks each journey with a single user, then
runs many of them at once and tells you where your product breaks.

> **Status: in development towards v1.0.** This README lists what works in
> the repository today. Everything else in the plan is marked **planned**.

## Get started

**Just the CLI.** One binary, no server, no database: write or generate a
scenario, run it, get a report.

```sh
# Linux and macOS
curl -fsSL https://raw.githubusercontent.com/Ivan825/Stampede/main/install.sh | sh
```

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/Ivan825/Stampede/main/install.ps1 | iex
```

The installers download the latest release for your OS and CPU (Linux,
macOS or Windows; amd64 or arm64), check its SHA-256 checksum and install
the single `stampede` binary. Nothing else is needed. Then:

```sh
stampede init --target http://localhost:3000   # detect the product type, install a pack, dry-run its journeys
stampede run stampede/<pack>/journeys/<file>.yaml -e TARGET_URL=http://localhost:3000 -o report.html
stampede                                        # or the interactive console: /init, /run spike, ...
```

**The full stack with the web UI.** Server, web UI, two workers and the
database, with Docker:

```sh
stampede up            # released images of the installed version
stampede setup         # create the organisation and the owner account
```

Everything is done with the CLI; the web UI on <http://localhost:8080> is
for analysis: live runs, reports, comparisons, scenarios, schedules and
settings, read only, plus the Stop, Kill and Kill all safety controls.

or, from a clone, the same stack plus **ShopLab**
(<http://localhost:8090>), a demo shop with six real performance problems
planted on purpose:

```sh
git clone https://github.com/Ivan825/Stampede && cd Stampede
docker compose up -d --build
```

The [quick start](docs/quickstart.md) takes you from there to a breakpoint,
a fix and a comparison in about five minutes.

**Everything from the terminal.** Every server feature has a command, each
with a table or summary and `--json` for scripts: `stampede setup` and
`login`, `users`, `tokens`, `projects`, `targets`, `secrets`, `push` and
`scenarios`, `start`, `runs`, `report`, `schedules`, `drift`, `ai`,
`notify`, `integrations`, `caps`, `settings` and `audit`. [Using Stampede
from the CLI](docs/guides/cli.md) goes from an empty server to scheduled,
monitored runs without the web UI.

**No internet access.** Each release attaches an offline bundle (images,
binary, Helm chart) for networks without internet access; see
[air-gapped installs](docs/deploy/airgap.md).

## What works today

**Describe users, not requests.** A [scenario](docs/concepts/scenarios.md)
is a YAML file of weighted journeys with `${}` expressions, extractors
(JSONPath, header, cookie, regex, CSS), checks (status, body, JSONPath
values, JSON Schema, latency, any expression), think times, branches,
loops, conditions, JavaScript `script` steps, and test data from CSV,
JSON, lists, ranges, generated fake data or a SQL query. It is validated
against a published JSON Schema, including that every variable is
extracted before it is used.

**Numbers you can trust.** Latency is measured from each request's
*scheduled* send time, so queueing is never hidden. Percentiles come from
merged HDR-style histograms, never averages of percentiles. DNS, connect,
TLS, wait and download are timed for every request. The load generator
watches itself (CPU, scheduling lag, dropped iterations, GC, file
descriptors, ephemeral ports, network), on workers and in `stampede run`
alike, and a run it distorted is marked generator-limited instead of
failed. Each kind of failure keeps redacted request/response examples
(browser steps: a screenshot, console errors and a HAR). Accuracy is checked against a calibrated server in CI
([how](docs/concepts/measurement.md)).

**Every test type.** Open and closed models; smoke, baseline, stress, spike,
soak, breakpoint, steps, recovery and wave shapes on any scenario; targets
such as `checkout.p95 < 800ms`; breakpoint search narrowed by
confirmation holds; the knee of the throughput-against-load curve; recovery
time after a spike; per-user network emulation (3G, 4G, slow
Wi-Fi, or explicit latency, jitter, bandwidth and packet loss); replay of
an access log or HAR file at its recorded times; auto-abort when a target
falls over ([test types](docs/guides/test-types.md)).

**Fault injection.** `stampede agent` sits next to your database, cache or
downstream services and, on a scenario's timeline, slows, throttles,
resets or blackholes their connections, pauses or stops containers, or
scales deployments down. Every fault is time-limited, audited and reverted
when the run ends, and the report shades the fault windows on its charts
([fault injection](docs/guides/faults.md)).

**Protocols:** HTTP/1.1, HTTP/2 (TLS and h2c), REST, GraphQL (with
persisted queries), WebSocket, server-sent events with time to first event
(for LLM apps), gRPC unary and server streaming via reflection or proto
files, and real browser pages in headless Chrome (click, fill, press,
assert) with Web Vitals: LCP, CLS, INP, first contentful paint and load
time ([protocols](docs/protocols.md)).

**Plugins** add step types for other protocols, each running as its own
process so a crash cannot take a worker down: `stampede plugin install
mqtt` (or `kafka`, `redis`, `sql`, `udp`), then steps such as `plugin:
mqtt.publish`. Their settings are checked against the plugin's schema,
their timings land in the same per-step histograms, and the target policy
applies to the addresses they connect to. A public SDK and conformance
suite let you write your own ([plugins](docs/plugins.md)). The first-party
plugins are tested against in-process fakes (Mochi MQTT, kfake, miniredis,
SQLite, a UDP echo listener), and a CI job runs the SQL plugin against
PostgreSQL and MySQL containers; none has been run against a production
cluster here.

**Distributed.** `stampede worker` connects out to the server over gRPC.
Load is split by capacity with no arrival lost or duplicated, start times are
synchronised to within a millisecond, results merge losslessly, a lost
worker's share is taken over by an idle worker (or the gap is marked in the
report when there is none), and a kill reaches every
worker in under a millisecond on a local network. With `--worker-mtls`
(the default in Compose and Helm) every worker gets its own certificate from
a CA built into the server, and the join token never crosses the network.

**Control plane and UI.** `stampede server` with PostgreSQL/TimescaleDB:
REST API ([OpenAPI](api/openapi.yaml)), projects, targets, encrypted secrets,
users and roles, single sign-on (OIDC), API tokens, audit log, integrations and
notification settings, all managed with the CLI, and a read-only web UI for
analysis: live runs and reports with target metrics and trace links,
comparisons, scenarios with their journey graph, schedules and settings, and
a kill switch that is always on screen.

**Reports and CI.** Self-contained HTML, PDF, CSV, JSON, JUnit XML and Markdown;
exit code 3 when a target fails. `stampede compare` judges repeated runs of
two versions with bootstrap confidence intervals and a measured noise floor,
and exits 4 on a regression ([comparing](docs/guides/comparing.md),
[CI](docs/guides/ci.md)). A composite GitHub Action in [`action/`](action/action.yml)
runs a scenario, writes the job summary, uploads the reports and fails the
step on a failed target; CI exercises it both with the latest release and
built from the commit.

**Scheduled runs.** Schedules, managed with `stampede schedules` or the API
(and shown read only in the web UI), start a saved scenario against a target on a cron expression in
UTC or any IANA time zone. Each firing gets the same checks as a run
started by hand, never overlaps the previous run, and fires at most once
however many replicas or restarts are involved
([scheduled runs](docs/guides/schedules.md)).

**Integrations.** An `observe` block charts your own Prometheus queries
(CPU, memory, connections) over the run in the report, and every step lists
its slowest requests with the W3C trace IDs they carried, as links to
Jaeger or Tempo given a link template. On the server, admins configure
Prometheus and trace integrations by name, so the server never fetches a
URL written in a scenario, and notification channels (signed webhooks,
Slack, Discord) hear when a run finishes, misses a target or is killed,
with retries, a delivery log and a guard against private destinations. The
server traces its API and runs with OpenTelemetry when an OTLP endpoint is
set, and a Grafana dashboard covers its own metrics
([integrations](docs/guides/integrations.md)).

**Terminal.** `stampede` alone opens a console: `/run spike`, `/run soak
4h`, `/run replay access.log`, `/init <url>`, `/runs`, `/kill all`, or
plain language such as "find the breaking point for checkout", always
confirmed before it runs.

**Product packs.** `stampede init` probes a target (its OpenAPI document,
OpenID Connect discovery, home page and headers) and installs the matching
pack. All twenty packs ship: e-commerce (tested against ShopLab), and SaaS
with GraphQL, AI and LLM apps over SSE, chat over WebSocket, ticketing
with a waiting room, login and identity, public APIs with rate limits,
fintech with duplicate-payment checks, social feeds with live
notifications, news behind a cache, HLS video and live streaming, exams
with a live channel, government results and applications, ride hailing
and food delivery with live tracking, mobile app backends, serverless
cold starts and concurrency limits, IoT over MQTT, event pipelines on
Kafka, databases and caches (PostgreSQL and Redis) and game backends
(WebSocket matchmaking, a UDP game server). Each is tested on every push
against a small reference app with planted bottlenecks
([PackLab](examples/packlab/README.md)); the last four use the protocol
plugins, against an in-process broker, Kafka cluster, Redis or UDP
server, and PostgreSQL in a service container ([packs](docs/guides/packs.md)).

**AI journey generation** (optional, bring your own key, never during
load). Anthropic, OpenAI, Gemini, Ollama or any OpenAI-compatible server
turns a description, an OpenAPI spec, a GraphQL schema, a HAR file, an
access log or a headless-browser crawl of the site into a scenario. Every journey is dry-run against your target and repaired until it
works before you approve it; recorded traffic is redacted before anything
reaches a model ([AI generation](docs/ai.md)). The pipeline is tested with a
scripted model; it has not yet been run against a real provider in this
repository's CI. `stampede run --narrative` (or `stampede report
--narrative`) adds a written summary to a finished report in which every
claim cites the figures behind it and is labelled measured or suspected.
`stampede coverage` shows which API endpoints no journey exercises, and
`stampede drift` finds journeys an API change broke ([coverage and
drift](docs/guides/coverage.md)).

**Safety.** Private targets just work; public targets stay under low caps
until you prove ownership; requests cannot leave the target's host; hard caps
per server and per target; audit log ([safety](docs/safety.md)).

## Planned for v1.0

wrk2 in the nightly side-by-side comparison (k6 is compared today, see
the [benchmarks](https://stampede.vercel.app/benchmarks)), connection-flood and
slow-client stresses, and the website.

## Deploy

| How | Guide | State |
|---|---|---|
| Docker Compose, one machine | [docs/deploy/compose.md](docs/deploy/compose.md) | `docker compose up -d` at the repository root |
| Kubernetes, Helm chart | [docs/deploy/helm.md](docs/deploy/helm.md) | tested on kind: install, `helm test`, a distributed run |
| Kubernetes operator (`StampedeCluster`, `StampedeRun`) | [docs/deploy/operator.md](docs/deploy/operator.md) | alpha; envtest-tested, and on kind in CI |
| Workers in several AWS/GCP regions, Terraform | [docs/deploy/terraform.md](docs/deploy/terraform.md) | validated, not yet applied |
| Air-gapped networks, offline bundle | [docs/deploy/airgap.md](docs/deploy/airgap.md) | a nightly CI job builds it, removes every image, and installs and starts Stampede from the bundle alone |
| Upgrades, backups, restores | [docs/deploy/upgrades.md](docs/deploy/upgrades.md) | |

Each [release](https://github.com/Ivan825/Stampede/releases) has binaries for Linux, macOS and Windows (amd64 and
arm64) with SBOMs and signed checksums, signed multi-arch images on
`ghcr.io/ivan825/stampede`, the Helm chart on `oci://ghcr.io/ivan825/charts`
and offline bundles. The latest is
[v1.0.0](https://github.com/Ivan825/Stampede/releases/tag/v1.0.0).

## Documentation

Everything is in [docs/](docs/README.md): quick start, concepts, guides
(among them [using Stampede from the CLI](docs/guides/cli.md)), the
[scenario reference](docs/reference/scenario.md), the [CLI
reference](docs/reference/cli/stampede.md) and
[configuration](docs/reference/configuration.md).

## Building from source

```sh
git clone https://github.com/Ivan825/Stampede
cd Stampede
make build        # bin/stampede
make test
go run ./bench/accuracy -quick
```

Requires Go 1.27 or later.

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md). Report vulnerabilities privately as
described in [SECURITY.md](SECURITY.md).

## Licence

Apache-2.0. See [LICENSE](LICENSE).
