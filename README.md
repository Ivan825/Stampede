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

## Two commands

```sh
git clone https://github.com/Ivan825/Stampede && cd Stampede
docker compose up -d --build
```

Open <http://localhost:8080> for the web UI. The stack includes two workers
and **ShopLab** (<http://localhost:8090>), a demo shop with six real
performance problems planted on purpose. The [quick start](docs/quickstart.md)
takes you from there to a breakpoint, a fix and a comparison in about five
minutes.

Just want the CLI?

```sh
go install github.com/Ivan825/Stampede/cmd/stampede@latest
stampede init --target http://localhost:8090      # detect the product type, install a pack, dry-run it
stampede run stampede/ecommerce/journeys/shop-mix.yaml -e TARGET_URL=http://localhost:8090 -o report.html
```

## What works today

**Describe users, not requests.** A [scenario](docs/concepts/scenarios.md)
is a YAML file of weighted journeys with `${}` expressions, extractors
(JSONPath, header, cookie, regex, CSS), checks, think times, branches,
loops, conditions and CSV/JSON/list/range data. It is validated against a
published JSON Schema, including that every variable is extracted before it
is used.

**Numbers you can trust.** Latency is measured from each request's
*scheduled* send time, so queueing is never hidden. Percentiles come from
merged HDR-style histograms, never averages of percentiles. DNS, connect,
TLS, wait and download are timed for every request. Workers report their own
saturation, and a run they distorted is marked generator-limited instead of
failed. Accuracy is checked against a calibrated server in CI
([how](docs/concepts/measurement.md)).

**Every test type.** Open and closed models; smoke, baseline, stress, spike,
soak, breakpoint, steps, recovery and wave shapes on any scenario; targets
such as `checkout.p95 < 800ms`; breakpoint search; the knee of the
throughput-against-load curve; per-user network emulation (3G, 4G, slow
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
worker is marked rather than silently ignored, and a kill reaches every
worker in under a millisecond on a local network. With `--worker-mtls`
(the default in Compose and Helm) every worker gets its own certificate from
a CA built into the server, and the join token never crosses the network.

**Control plane and UI.** `stampede server` with PostgreSQL/TimescaleDB:
REST API ([OpenAPI](api/openapi.yaml)), web UI with a scenario editor and
journey graph, live runs and reports, projects, targets, encrypted secrets,
users and roles, single sign-on (OIDC), API tokens, audit log, integrations and notification settings,
reports with target metrics and trace links, and a kill switch that is always on
screen.

**Reports and CI.** Self-contained HTML, PDF, CSV, JSON, JUnit XML and Markdown;
exit code 3 when a target fails. `stampede compare` judges repeated runs of
two versions with bootstrap confidence intervals and a measured noise floor,
and exits 4 on a regression ([comparing](docs/guides/comparing.md),
[CI](docs/guides/ci.md)). A composite GitHub Action in [`action/`](action/action.yml)
runs a scenario, writes the job summary, uploads the reports and fails the
step on a failed target; CI exercises it by building from source (release
downloads are untested until a release is published).

**Scheduled runs.** Schedules, managed from the web UI, `stampede schedules`
or the API, start a saved scenario against a target on a cron expression in
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

**Terminal.** `stampede` alone opens a console: `/run spike`, `/runs`,
`/kill all`, or plain language such as "find the breaking point for
checkout", always confirmed before it runs.

**Product packs.** `stampede init` probes a target (its OpenAPI document,
OpenID Connect discovery, home page and headers) and installs the matching
pack. Eleven packs ship: e-commerce (tested against ShopLab), and SaaS with
GraphQL, AI and LLM apps over SSE, chat over WebSocket, ticketing with a
waiting room, login and identity, public APIs with rate limits, IoT over
MQTT, event pipelines on Kafka, databases and caches (PostgreSQL and
Redis) and game backends (WebSocket matchmaking, a UDP game server), each
tested on every push against a small reference app with planted
bottlenecks ([PackLab](examples/packlab/README.md)); the last four use the
protocol plugins, against an in-process broker, Kafka cluster, Redis or
UDP server, and PostgreSQL in a service container. The other 9 product
types are catalogued as planned ([packs](docs/guides/packs.md)).

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

The other 9 product packs, side-by-side benchmarks with k6 and wrk2, and
the website.

## Deploy

| How | Guide | State |
|---|---|---|
| Docker Compose, one machine | [docs/deploy/compose.md](docs/deploy/compose.md) | `docker compose up -d` at the repository root |
| Kubernetes, Helm chart | [docs/deploy/helm.md](docs/deploy/helm.md) | tested on kind: install, `helm test`, a distributed run |
| Kubernetes operator (`StampedeCluster`, `StampedeRun`) | [docs/deploy/operator.md](docs/deploy/operator.md) | alpha, envtest-tested |
| Workers in several AWS/GCP regions, Terraform | [docs/deploy/terraform.md](docs/deploy/terraform.md) | validated, not yet applied |
| Air-gapped networks, offline bundle | [docs/deploy/airgap.md](docs/deploy/airgap.md) | built and installed from scratch by a nightly CI job |
| Upgrades, backups, restores | [docs/deploy/upgrades.md](docs/deploy/upgrades.md) | |

Releases (binaries for Linux, macOS and Windows, signed multi-arch images
on `ghcr.io/ivan825/stampede`, Homebrew and Scoop) are built by GoReleaser
from version tags; none has been published yet.

## Documentation

Everything is in [docs/](docs/README.md): quick start, concepts, guides, the
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
