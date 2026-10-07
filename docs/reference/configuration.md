# Configuration

## Server (`stampede server`)

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `--addr` | `STAMPEDE_ADDR` | `:8080` | HTTP listen address (API, UI, `/metrics`, `/healthz`, `/readyz`) |
| `--database-url` | `STAMPEDE_DATABASE_URL` | — | PostgreSQL URL; migrations run on start |
| — | `STAMPEDE_MASTER_KEY` | — | 32 random bytes, base64 (`stampede keygen`); encrypts secrets and AI keys |
| — | `STAMPEDE_MASTER_KEY_FILE` | — | read the key from a file instead |
| — | `STAMPEDE_MASTER_KEY_COMMAND` | — | run this shell command and use what it prints, so the key can come from a cloud KMS or secret manager, for example `gcloud secrets versions access latest --secret=stampede-master-key` or `aws secretsmanager get-secret-value --secret-id stampede-master-key --query SecretString --output text` (30-second limit) |
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
| `--tls-cert`, `--tls-key` | `STAMPEDE_TLS_CERT`, `STAMPEDE_TLS_KEY` | — | serve the API and web UI over HTTPS with this certificate and key (PEM); turns on secure cookies |
| `--acme-domain` | `STAMPEDE_ACME_DOMAINS` (comma separated) | — | serve HTTPS with certificates from Let's Encrypt for these host names, renewed automatically; the server must be reachable on port 443 under them (`--addr :443`) |
| `--acme-email`, `--acme-cache` | `STAMPEDE_ACME_EMAIL`, `STAMPEDE_ACME_CACHE` | —, `<data-dir>/acme` | the Let's Encrypt account contact and where its keys and certificates are kept (keep this directory across restarts) |
| `--http-redirect-addr` | `STAMPEDE_HTTP_REDIRECT_ADDR` | — | with HTTPS, also listen for plain HTTP (for example `:80`), redirect it to HTTPS and answer ACME HTTP-01 challenges |
| `--public-url` | `STAMPEDE_PUBLIC_URL` | — | external URL of the web UI; notifications link to `<url>/runs/<id>` when set |
| `--ha` | `STAMPEDE_HA` | `standby` | how server replicas share the work: `standby` (one serves, the others wait) or `active` (all serve; see [Helm](../deploy/helm.md#server-replicas)) |
| `--scheduler-interval` | `STAMPEDE_SCHEDULER_INTERVAL` | `15s` | how often a serving replica looks for due [schedules](../guides/schedules.md); `0` disables scheduled runs |
| `--metrics-retention` | `STAMPEDE_METRICS_RETENTION` | `0` (keep forever) | how long per-second run metrics are kept, such as `30d`; at least `1d`. See below |
| `--log-level`, `--log-format` | `STAMPEDE_LOG_LEVEL`, `STAMPEDE_LOG_FORMAT` | `info`, `json` | logging |
| `--migrate-only`, `--migrate-dry-run` | — | — | apply or report migrations, then exit |

### Run metrics: rollups and retention

Runs record one row of metrics per second. Migration 11 adds two rollups,
`run_metrics_10s` and `run_metrics_1m`, served by
`GET /api/v1/runs/{runId}/timeline?resolution=10s` (or `1m`): requests
and errors summed, rate averaged, p50 the mean and p95/p99 the worst
per-second value of each bucket. Exact quantiles stay in the run's report.

- With TimescaleDB (the Compose stack and Helm chart) the rollups are
  continuous aggregates, refreshed every minute, and `--metrics-retention`
  becomes a retention policy that drops whole chunks of per-second rows.
  Rollups and reports are kept.
- On plain PostgreSQL the rollups are views over the per-second rows, and
  the serving replica deletes rows older than the retention every hour, so
  the rollups lose them too. Reports are kept.

With the default `0` nothing is deleted.

Back up the master key with the database: secrets cannot be decrypted
without it. See [upgrades and backups](../deploy/upgrades.md).

Integrations (Prometheus, trace links) and notification channels are
configured per organisation with the CLI (`stampede integrations create`,
`stampede notify channels create`) or the API, not here, and listed read
only under Settings in the web UI; see
[integrations](../guides/integrations.md).

### Single sign-on (OpenID Connect)

People can sign in through your identity provider (Okta, Microsoft Entra
ID, Google, Keycloak, Auth0, Dex or any OpenID Connect provider) as well as
with a password.

| Flag | Environment | Meaning |
|---|---|---|
| `--oidc-issuer` | `STAMPEDE_OIDC_ISSUER` | the provider's issuer URL; turns SSO on |
| `--oidc-client-id` | `STAMPEDE_OIDC_CLIENT_ID` | the client registered for Stampede |
| | `STAMPEDE_OIDC_CLIENT_SECRET` | its secret (environment only, so it stays out of the process list) |
| `--oidc-redirect-url` | `STAMPEDE_OIDC_REDIRECT_URL` | default `--public-url` + `/api/v1/auth/oidc/callback`; register it with the provider |
| `--oidc-name` | `STAMPEDE_OIDC_NAME` | label of the sign-in button (default `SSO`) |
| `--oidc-allowed-domain` | `STAMPEDE_OIDC_ALLOWED_DOMAINS` | email domains that may sign in (repeatable or comma-separated; default any) |
| `--oidc-default-role` | `STAMPEDE_OIDC_DEFAULT_ROLE` | role for someone signing in for the first time: `viewer`, `runner`, `editor` or `admin`. Empty (the default) lets only people who already have an account sign in. |

How it works:

- The sign-in page shows *Sign in with <name>*. Stampede uses the
  authorization code flow with PKCE, a `state` bound to a cookie and a
  `nonce`, and verifies the ID token's signature, issuer, audience and
  expiry against the provider's published keys.
- The account is matched by email. The provider must report the email as
  verified (`email_verified`), and its domain must be allowed.
- With a default role, a first sign-in creates the account in the
  organisation with that role; an admin can change it later, and the
  change is kept on later sign-ins. Accounts created this way have no
  password. Without a default role, an admin invites people first (Settings
  → Users); they can then sign in with SSO or a password.
- Sign-ins and account creation are written to the audit log.
- API tokens are unaffected: CLI and CI use tokens, not SSO.

`GET /api/v1/auth/config` (public) reports whether SSO is on and where it
starts. Admins see the whole configuration, read-only and without the
client ID or secret, in Settings → SSO (`GET /api/v1/settings/sso`); the
caps runs are checked against (the hard caps above, the organisation's
caps, each project's caps and dry-run gate, the caps for unverified public
targets, the abort floor and each target's own and effective caps) are in
Settings → Limits (`GET /api/v1/settings/limits`).

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
| `--server a:8081,b:8081`, `--server dns:host:8081` | with active server replicas, every replica's worker port, listed or found through DNS (re-resolved every 30 seconds); the worker runs load for one replica at a time |
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
