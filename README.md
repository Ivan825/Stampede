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

## What works today

- **One binary.** `stampede run` runs a scenario with an in-process engine.
  No server, database or account needed.
- **Scenario files** in YAML or JSON: weighted journeys, `${}` expressions,
  extractors (JSONPath, header, cookie, regex, CSS), checks, think times,
  branches, loops, conditions, and CSV/JSON/list/range data feeders.
- **Open and closed load models.** Fixed arrival rate (`mode: rate`) or a
  fixed number of users (`mode: vus`), constant or ramping, plus fixed
  iteration counts.
- **Traffic shapes:** smoke, baseline, stress, spike, soak, breakpoint, step
  ramp, recovery and wave.
- **Measurement you can trust.** Latency is measured from each request's
  *scheduled* send time, so queueing is never hidden (coordinated omission).
  Percentiles come from merged HDR-style histograms, never averaged. Per
  phase timing: DNS, connect, TLS, time to first byte, download.
- **Targets and verdicts:** `http.p95 < 500ms`, `errors < 1%`,
  `checkout.p99 < 1s`. Breakpoint runs report the highest load that held.
- **Reports:** terminal summary, self-contained HTML, JSON, JUnit XML and
  Markdown. Exit code 3 when a target fails, for CI.
- **Safety:** private and loopback targets just work; public targets run
  under low caps until you verify ownership (`stampede target verify`).
  Requests can only reach the target host unless you allow others.
- **Accuracy benchmark** against a calibrated server with exact ground truth
  (`bench/`).

## Planned for v1.0

Control plane with REST API and database, distributed workers over gRPC,
web UI, terminal UI, AI journey generation with dry-run validation, HTTP/2
tuning, GraphQL, WebSocket, SSE, gRPC and browser drivers, plugins, product
packs, Docker Compose stack with the ShopLab demo app, Helm chart and
operator, comparison of releases, and the website.

## Quick start

```sh
go install github.com/Ivan825/Stampede/cmd/stampede@latest
```

Write `smoke.yaml`:

```yaml
apiVersion: stampede.dev/v1
kind: Scenario
metadata:
  name: shop-smoke
target:
  baseURL: ${env.TARGET_URL}
journeys:
  - name: browse
    weight: 9
    steps:
      - get: /api/products?page=${rand(1, 20)}
        check: { status: 200 }
        extract: { productId: "$.items[0].id" }
      - think: 1s..3s
      - get: /api/products/${productId}
  - name: login
    weight: 1
    steps:
      - post: /api/login
        json: { email: "${data.users.email}", password: "${data.users.password}" }
        extract: { token: "$.token" }
      - get: /api/me
        headers: { Authorization: "Bearer ${token}" }
data:
  users: { csv: users.csv, mode: unique }
load:
  mode: rate
  rate: 50/s
  duration: 2m
targets:
  - http.p95 < 300ms
  - errors < 1%
```

Run it:

```sh
stampede run smoke.yaml -e TARGET_URL=http://localhost:8090 -o report.html
stampede run smoke.yaml --shape spike --rate 20/s     # same journeys, spike shape
stampede validate smoke.yaml                          # check without running
```

## Scenario reference (short)

| Key | Meaning |
|---|---|
| `get/post/put/patch/delete/head/options: <path>` | An HTTP request. Relative paths join `target.baseURL`. |
| `headers`, `query`, `json`, `body`, `form` | Request parts. Values may contain `${}` expressions. |
| `check` | `status` (200, [200, 201], "2xx"), `bodyContains`, `json` (path → value or `exists`), `maxLatency`, `expr`. |
| `extract` | `name: "$.path"`, `header:Name`, `cookie:name`, `regex:(...)`, `css:selector[@attr]`, `status`, `body`. |
| `think: 2s..6s` | Pause, fixed or uniformly random. |
| `branch` | Weighted alternatives, each with its own steps. |
| `loop: 3` / `while: expr` | Repeat steps. |
| `group: name` | Name a set of steps. |
| `if: expr` | Skip a step unless the expression is true. |

Expressions are [CEL](https://cel.dev) with helpers: `rand(a, b)`,
`randString(n)`, `randEmail()`, `uuid()`, `pick(list)`, `now()`, `nowMs()`,
`base64(s)`, `urlencode(s)`, `sha256(s)`, `toJSON(v)`. Roots: `env`,
`secret`, `data`, `vars`, `vu`, `iter`, plus every extracted variable.

A failed step ends that iteration, as a real user would not carry on after
an error. Failed requests and checks count as errors.

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
