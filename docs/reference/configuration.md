# Configuration

## Server (`stampede server`)

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `--addr` | `STAMPEDE_ADDR` | `:8080` | HTTP listen address (API, UI, `/metrics`, `/healthz`, `/readyz`) |
| `--database-url` | `STAMPEDE_DATABASE_URL` | — | PostgreSQL URL; migrations run on start |
| — | `STAMPEDE_MASTER_KEY` | — | 32 random bytes, base64 (`stampede keygen`); encrypts secrets and AI keys |
| — | `STAMPEDE_MASTER_KEY_FILE` | — | read the key from a file instead |
| — | `STAMPEDE_MASTER_KEY_AUTOGEN` | `false` | with `_FILE`, create the key file on first start (Compose does this) |
| `--worker-addr` | `STAMPEDE_WORKER_ADDR` | `:8081` | gRPC port workers connect to |
| `--join-token` | `STAMPEDE_JOIN_TOKEN` | — | shared secret workers must present; workers are disabled without it |
| `--worker-tls-cert`, `--worker-tls-key` | `STAMPEDE_WORKER_TLS_CERT`, `_KEY` | — | TLS for the worker port |
| `--executor` | `STAMPEDE_EXECUTOR` | `auto` | `auto`: workers when any are idle, else in-process; `workers`; `local` |
| `--data-dir` | `STAMPEDE_DATA_DIR` | — | folder holding CSV/JSON feeder files; file feeders are refused without it |
| `--max-rate`, `--max-vus`, `--max-duration` | — | none | hard caps applied to every run |
| `--abort-errors`, `--abort-for` | `STAMPEDE_ABORT_ERRORS` | `90%`, `30s` | stop any run whose error rate stays at or above this; `0` disables |
| `--trusted-proxy` | `STAMPEDE_TRUSTED_PROXIES` (comma separated) | none | reverse proxies whose `X-Forwarded-For` is believed |
| `--secure-cookies` | `STAMPEDE_SECURE_COOKIES=true` | `false` | mark the session cookie Secure (behind HTTPS) |
| `--public-url` | `STAMPEDE_PUBLIC_URL` | — | external URL of the web UI; notifications link to `<url>/runs/<id>` when set |
| `--log-level`, `--log-format` | `STAMPEDE_LOG_LEVEL`, `STAMPEDE_LOG_FORMAT` | `info`, `json` | logging |
| `--migrate-only`, `--migrate-dry-run` | — | — | apply or report migrations, then exit |

Back up the master key with the database: secrets cannot be decrypted
without it. See [upgrades and backups](../deploy/upgrades.md).

Integrations (Prometheus, trace links) and notification channels are
configured per organisation through the API or Settings in the web UI, not
here; see [integrations](../guides/integrations.md).

### Tracing (OpenTelemetry)

The server traces its own HTTP API and every run with OpenTelemetry. Tracing
is off unless the standard OTLP exporter variables are set; there is
nothing Stampede-specific to configure.

| Environment | Meaning |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` (or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`) | turns tracing on and sets the collector, e.g. `http://otel-collector:4318` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` (or `..._TRACES_PROTOCOL`) | `http/protobuf` (default) or `grpc` (then use port 4317) |
| `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_EXPORTER_OTLP_INSECURE`, ... | read by the OpenTelemetry SDK as usual |
| `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES` | override the default `service.name=stampede-server` |
| `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG` | sampling (default: parent-based, always on) |

What is traced:

- one span per API request (`otelhttp`), named after the route, such as
  `POST /api/v1/projects/{projectId}/runs`, with the `http.route` attribute;
  incoming W3C `traceparent` headers are honoured;
- one span per run, named `run`, from scheduling to the stored report. It is
  the root of its own trace and links to the request that started the run.
  Attributes: `stampede.run.id`, `stampede.project.id`, `stampede.scenario`,
  `stampede.target`, `stampede.load.mode`, `stampede.load.peak`,
  `stampede.run.status`, `stampede.run.verdict`, `stampede.requests`,
  `stampede.error_rate`, `stampede.p95_seconds`; events mark when load
  started and ended. A run that fails sets the span's status to error.

The load itself is not traced by this: virtual users send their own W3C
`traceparent` to your target so you can find their requests in your
tracing system (see [integrations](../guides/integrations.md#trace-links)).

Workers and `stampede run` do not export spans.

### Metrics and the Grafana dashboard

`GET /metrics` (no authentication; keep it on an internal network) serves
Prometheus metrics:

| Metric | Meaning |
|---|---|
| `stampede_runs_active` | runs executing now |
| `stampede_runs_finished_total{status}` | finished runs by status: `completed`, `aborted`, `failed` |
| `stampede_http_request_duration_seconds{method,route,code}` | API latency histogram (live run streams excluded) |
| `go_*`, `process_*` | Go runtime and process metrics |

[`deploy/grafana/stampede-server.json`](../../deploy/grafana/stampede-server.json)
is a Grafana dashboard for them: runs active and finished, API request
rate, latency percentiles, 5xx ratio and the slowest routes, and CPU,
memory, goroutines, garbage collection and file descriptors. Import it in
Grafana (Dashboards → New → Import) and pick your Prometheus data source; the
`job` and `instance` variables select the servers. A test checks that every
`stampede_*` and `go_*` metric the dashboard queries is served by
`/metrics`.

## Worker (`stampede worker`)

| Flag | Meaning |
|---|---|
| `--server host:8081` | the server's worker port (workers connect out, so they work behind NAT) |
| `--token` / `STAMPEDE_JOIN_TOKEN` | the join token |
| `--name`, `--region`, `--label k=v` | identity; regions let a run split load by region |
| `--max-vus` | the most users this worker accepts |
| `--insecure` or `--ca file.pem` | plain gRPC on trusted networks, or TLS with a private CA |

A worker that loses the server for 10 seconds during a run stops its load
on its own.

## Plugins

| Variable | Meaning |
|---|---|
| `STAMPEDE_PLUGIN_DIR` | where plugins are installed and looked for first (default `plugins/` under the user config folder); then `PATH` is searched for `stampede-plugin-<name>` |
| `STAMPEDE_SOURCE` | a Stampede checkout that `stampede plugin install <first-party name>` builds from |
| `STAMPEDE_PLUGIN_REPO` | the repository cloned when there is no checkout (default `https://github.com/Ivan825/Stampede`) |

The CLI, workers and the server (for runs it executes in-process) each look
for plugins this way. See [plugins](../plugins.md).

## CLI

`stampede login` stores the server URL and an API token in the user config
folder (`~/.config/stampede/config.yaml` on Linux,
`~/Library/Application Support/stampede/config.yaml` on macOS).
`STAMPEDE_SERVER` and `STAMPEDE_TOKEN` override it.

`stampede run` passes the process environment to `${env.X}`, and to
`${secret.X}` (also as `STAMPEDE_SECRET_X`). On the server, `${env.X}` comes
only from the run's own environment and `${secret.X}` from the project's
stored secrets; the server's own environment is never exposed to scenarios.
