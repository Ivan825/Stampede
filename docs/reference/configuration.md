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
| `--ha` | `STAMPEDE_HA` | `standby` | how server replicas share the work: `standby` (one serves, the others wait) or `active` (all serve; see [Helm](../deploy/helm.md#server-replicas)) |
| `--scheduler-interval` | `STAMPEDE_SCHEDULER_INTERVAL` | `15s` | how often a serving replica looks for due [schedules](../guides/schedules.md); `0` disables scheduled runs |
| `--log-level`, `--log-format` | `STAMPEDE_LOG_LEVEL`, `STAMPEDE_LOG_FORMAT` | `info`, `json` | logging |
| `--migrate-only`, `--migrate-dry-run` | — | — | apply or report migrations, then exit |

Back up the master key with the database: secrets cannot be decrypted
without it. See [upgrades and backups](../deploy/upgrades.md).

Integrations (Prometheus, trace links) and notification channels are
configured per organisation through the API or Settings in the web UI, not
here; see [integrations](../guides/integrations.md).

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
starts.

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
