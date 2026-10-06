# Integrations

Stampede connects a load test to the rest of your tooling:

- **Target metrics from Prometheus**: after a run, your own PromQL queries
  are evaluated over the run's time range and charted in the report next to
  Stampede's numbers, so CPU, memory, queue depth or database connections
  line up with the load that caused them.
- **Trace links**: every request carries a W3C `traceparent`. The report
  lists the slowest requests of each step with their trace IDs, and with a
  Jaeger or Tempo link template those IDs become links.
- **Notifications** (server): a generic signed webhook, Slack or Discord
  hears when a run finishes, misses a target or is killed.
- **CI**: the [GitHub Action](ci.md#github-action).
- **Stampede's own telemetry**: OpenTelemetry traces of the server and a
  Grafana dashboard for its metrics; see
  [configuration](../reference/configuration.md#tracing-opentelemetry).

## Target metrics from Prometheus

### With `stampede run`

Add an `observe` block to the scenario:

```yaml
observe:
  prometheus:
    url: ${env.PROM_URL}                 # http://prometheus:9090; may be templated
    bearerToken: ${secret.PROM_TOKEN}    # optional; STAMPEDE_SECRET_PROM_TOKEN
    queries:
      cpu: 'rate(process_cpu_seconds_total{job="shop"}[30s])'
      memory: 'process_resident_memory_bytes{job="shop"}'
      db_connections: 'sum(pg_stat_activity_count{datname="shop"})'
```

After the run, each query is sent to `/api/v1/query_range` (Prometheus,
Thanos, Mimir, VictoriaMetrics and other compatible servers) from the run's
start to its end, with a step equal to the report interval (one second).
Runs long enough to exceed 10,000 points per query use a coarser step, as
Prometheus refuses more than 11,000.

The results appear:

- in the **terminal summary** as min, max and last per query;
- in the **HTML report** as one chart per query, under *Target metrics*;
- in the **JSON report** as `targetMetrics`:

  ```json
  "targetMetrics": [
    { "name": "cpu", "query": "rate(process_cpu_seconds_total{job=\"shop\"}[30s])",
      "points": [ { "t": 0, "value": 0.21 }, { "t": 1, "value": 0.34 } ] },
    { "name": "db_connections", "query": "...", "points": [],
      "error": "prometheus: bad_data: 1:5: parse error: ..." }
  ]
  ```

  `t` is seconds since the run started, like the timeline. A query that
  fails, returns nothing or cannot be reached keeps its `error` and never
  fails the run. A query returning several series is charted by its first
  series, with an `error` note asking you to aggregate it (`sum(...)`,
  `max(...)`).

Notes:

- Queries run once the run has ended; the last scrape interval of the run
  may not be in Prometheus yet.
- Values that are `NaN` or infinite are left out.
- Up to 20 queries; names are letters, digits, `_`, `.` and `-`.

### On the server

A server never contacts a URL written in a scenario: that would let anyone
who can edit a scenario make the server fetch internal addresses. Instead an
admin configures named **integrations** for the organisation, and scenarios
refer to them by name:

```yaml
observe:
  prometheus:
    integration: prod-prometheus
    queries:
      cpu: 'rate(process_cpu_seconds_total{job="shop"}[30s])'
  traces:
    integration: jaeger
```

Add integrations under **Settings → Integrations** in the web UI (admins
only; every member can see their names), or with the API:

```sh
curl -X POST $STAMPEDE_SERVER/api/v1/integrations -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"prod-prometheus","kind":"prometheus","url":"http://prometheus:9090","bearerToken":"..."}'
```

The bearer token is encrypted with the server's master key and never
returned. A run whose scenario names `observe.prometheus.url` instead of an
integration, or an integration that does not exist, is refused when it is
started (HTTP 422). The server strips the observe block before sending the
scenario to workers; it queries Prometheus itself once the run ends.

## Trace links

Every request Stampede sends carries a W3C `traceparent` header; all
requests of one journey iteration share a trace ID. If your services
propagate and sample it, the iteration appears in your tracing system as one
trace.

For each step the report keeps the **five slowest requests** with their
latency (from the scheduled send time), when they were sent, their status
or error, and their trace ID. Distributed runs keep the five slowest across
all workers. They appear in the JSON report (`journeys[].steps[].slowest`),
the HTML report and the web UI, and the five slowest overall in the
terminal summary.

To turn trace IDs into links, give a link template containing `{traceId}`:

```yaml
observe:
  traces:
    url: https://jaeger.example.com/trace/{traceId}
```

Any `http(s)` URL with `{traceId}` in it works, so the link can point at
Jaeger, a Tempo-backed Grafana view or any other trace UI.

On the server, use `integration: <name>` with a **traces** integration, or
`url:` directly: nothing is fetched from it, it only builds links. The
template must be an `http(s)` URL.

A request whose scenario sets its own `traceparent` header keeps that
header; the report still shows the trace ID Stampede generated for the
iteration, which then does not match.

## Notifications

Notification channels belong to the organisation and are managed by admins
under **Settings → Notifications** or the API (`/api/v1/notifications/channels`).

| Kind | Body |
|---|---|
| `webhook` | the event as JSON (below), signed |
| `slack` | a Slack incoming webhook message (`text` in mrkdwn) |
| `discord` | a Discord webhook message (`content`; mentions disabled) |

Events (a channel subscribes to any of them; all by default):

| Event | When |
|---|---|
| `run.finished` | a run ended: completed, aborted or failed to run; includes the verdict and summary |
| `run.target_failed` | a run completed and missed a target; lists the failed targets |
| `run.killed` | a run was stopped with the kill switch; names who pulled it |

A generic webhook receives:

`id` is the same for every retry of one delivery.

```json
{
  "id": "5b0c...",
  "type": "run.target_failed",
  "at": "2026-10-06T12:00:00Z",
  "organisation": "Acme",
  "message": "checkout missed 1 of 2 targets",
  "run": {
    "id": "...", "project": "Shop", "scenario": "checkout", "target": "https://staging.shop.example",
    "status": "completed", "verdict": "fail", "stopReason": "completed",
    "url": "https://stampede.example.com/runs/...",
    "summary": { "requests": 12000, "errorRate": 0.004, "rps": 200, "p95": 0.81, "p99": 1.4 },
    "failedTargets": ["checkout.p95 < 500ms"]
  }
}
```

`run.url` is set when the server knows its public URL (`--public-url` /
`STAMPEDE_PUBLIC_URL`).

### Verifying webhook signatures

Each webhook request has these headers:

| Header | Value |
|---|---|
| `X-Stampede-Signature` | `sha256=` and the hex HMAC-SHA256 of the raw body, keyed with the channel's secret |
| `X-Stampede-Event` | the event type |
| `X-Stampede-Delivery` | the delivery ID (same as the body's `id`) |
| `X-Stampede-Timestamp` | Unix seconds when the attempt was sent |

The secret is generated when the channel is created (or you supply one of
at least 16 characters) and is shown only once. Verify in constant time,
for example in Go:

```go
mac := hmac.New(sha256.New, []byte(secret))
mac.Write(body)
ok := hmac.Equal([]byte("sha256="+hex.EncodeToString(mac.Sum(nil))), []byte(r.Header.Get("X-Stampede-Signature")))
```

### Delivery, retries and the log

Deliveries run in the background after a run ends. A transport error, a
timeout, `408`, `425`, `429` or a `5xx` response is retried after 2, 10 and
30 seconds (a longer `Retry-After`, up to a minute, is honoured); other
responses are not retried. Redirects are not followed. Every attempt is
recorded, and the last 50 attempts per channel are kept: the **delivery
log** in Settings → Notifications, or
`GET /api/v1/notifications/channels/{id}/deliveries`.

**Send test** (`POST /api/v1/notifications/channels/{id}/test`) sends a
`test` event once, without retries, and returns the attempt.

### Private destinations

To keep the server from being used to reach internal services, a channel's
destination may not be a loopback, private (RFC 1918, unique local),
link-local (including cloud metadata at `169.254.169.254`), carrier-grade
NAT, multicast or reserved address, nor `localhost` or a `.local` or
`.internal` name. The check runs when the channel is created and again on
every connection, against the address actually dialled, so a DNS change
cannot get around it; proxies from the environment are not used.

An admin can tick **Allow private destinations** for a channel that must
reach an internal receiver, such as a chat bridge inside your network.

Channel URLs (which for Slack and Discord are themselves credentials) and
signing secrets are encrypted with the master key; the API only ever
returns the scheme and host. Creating channels therefore needs
`STAMPEDE_MASTER_KEY`.

## API

All endpoints are under the `integrations` tag in the
[OpenAPI document](../../api/openapi.yaml). Listing integrations needs any
role; everything else needs admin, and every change is audited.

| Method and path | |
|---|---|
| `GET /integrations` | list (tokens are never returned) |
| `POST /integrations` | create `{name, kind: prometheus\|traces, url, bearerToken?}` |
| `DELETE /integrations/{id}` | delete |
| `GET /notifications/channels` | list, with each channel's last delivery |
| `POST /notifications/channels` | create `{name, kind: webhook\|slack\|discord, url, events?, allowPrivate?, secret?}`; returns `{channel, secret?}` |
| `DELETE /notifications/channels/{id}` | delete |
| `POST /notifications/channels/{id}/test` | send a test now; returns the attempt |
| `GET /notifications/channels/{id}/deliveries?limit=` | the delivery log, newest first |
