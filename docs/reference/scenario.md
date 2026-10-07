# Scenario reference

The authoritative definition is the [JSON Schema](../../schema/scenario.schema.json);
`stampede validate file.yaml` checks a file, and `stampede validate
file.yaml --dry-run -e TARGET_URL=...` also runs each journey once with one
user against the target and reports which ones pass. This page lists every
field.

## Top level

| Field | Type | Notes |
|---|---|---|
| `apiVersion` | `stampede.dev/v1` | default |
| `kind` | `Scenario` | default |
| `metadata.name` | string | lowercase letters, digits, `.`, `_`, `-` |
| `metadata.description`, `metadata.tags` | string, list | |
| `target.baseURL` | string | joined with relative paths; may use `${env.X}`; on the server the run's target replaces it |
| `target.headers` | map | sent with every request |
| `target.timeout` | duration | per request, default 30s |
| `target.verify` | `dns-txt` \| `well-known` | |
| `target.http.connections` | `per-vu` \| `shared` | per-user keep-alive pools (default) or one shared pool |
| `target.http.http2` / `h2c` | bool | HTTP/2 over TLS / cleartext HTTP/2 with prior knowledge |
| `target.http.disableKeepAlive`, `insecureSkipVerify` | bool | |
| `target.http.maxRedirects` | int | 0 = 10, -1 = do not follow |
| `target.http.dnsCacheTTL` | duration | default 30s, `0s` disables |
| `target.http.tlsResumption` | `per-vu` \| `shared` \| `off` | where TLS sessions are resumed from: each user's own (default with per-vu connections, like separate browsers), any user's (default with shared connections), or never |
| `target.network` | `{profile, rtt, jitter, loss, down, up}` | emulate a slower network per user; profiles `slow-3g 3g 4g slow-wifi` |
| `vars` | map | static variables |
| `data.<name>` | feeder | `csv` \| `json` \| `list` \| `range` \| `generate` \| `sql`, `mode`, `onExhausted` |
| `data.<name>.generate` | map | field name to kind; a new row for every use (see [test data](../concepts/scenarios.md#test-data)) |
| `data.<name>.sql` | `{driver, dsn, query, limit}` | `driver` is `postgres` or `mysql`; `dsn` may use `${env.X}`, `${secret.X}`; rows are read when the run starts (at most 1,000,000) |
| `journeys[]` | | `name`, `weight` (default 1), `tags`, `target` (p50/p90/p95/p99/errors), `steps` |
| `load` | | see below |
| `targets[]` | string | `[scope.]metric op value` |
| `observe.prometheus` | `{url \| integration, bearerToken?, queries}` | PromQL queries charted in the report after the run; `url` (may use `${env.X}`) for `stampede run`, `integration` (a server integration's name) on the server; up to 20 `name: query` pairs ([integrations](../guides/integrations.md)) |
| `observe.traces` | `{url \| integration}` | link template containing `{traceId}` for the slowest requests' traces |
| `faults.agent` | `{url, token}` \| `{integration}` | a `stampede agent`'s control API; `url` and `token` (may use `${env.X}`, `${secret.X}`) for `stampede run`, an agent integration on the server ([fault injection](../guides/faults.md)) |
| `faults.timeline[]` | | `name`, `at`, `for`, and one of `proxy` (`latency`, `jitter`, `bandwidth`, `reset`, `refuse`, `blackhole`), `container` (`action`: pause, stop, kill, restart) or `deployment` (`replicas`) |

## Load

| Field | Notes |
|---|---|
| `mode` | `vus` (closed), `rate` (open) or `replay`; `rate:` alone implies `rate` |
| `replay` | with `mode: replay`: `{file, format (auto, log, har), speed, limit, host, static}`; journeys come from the recording ([replay](../guides/test-types.md#replaying-recorded-traffic)) |
| `vus`, `rate`, `duration` | constant load |
| `stages[]` | `{duration, target}`; target is users or a rate |
| `shape` | `smoke baseline stress spike soak breakpoint steps recovery wave` |
| `start`, `max` | shape levels |
| `steps`, `stepDuration`, `cycles` | breakpoint/steps and wave tuning |
| `iterations` | fixed total iterations shared by `vus` users |
| `maxVUs` | open-model user cap (default 5 × peak rate, 50 to 50,000) |
| `gracefulStop` | time in-flight iterations get at the end (default 30s) |
| `abort` | `{errors: 50%, p95: 5s, for: 10s}`: stop early when either limit holds for `for` (default 10s) |

Units: durations `500ms 2s 1m30s 4h 2d` (a bare number is seconds); rates
`50/s 3000/m 100/h`; percentages `1%` or `0.01`.

## Steps

See [scenarios](../concepts/scenarios.md) for HTTP, think, branch, loop,
while, group, `if`, checks and extractors, [protocols](../protocols.md)
for `graphql`, `ws`, `sse` and `grpc`, and [plugins](../plugins.md) for
`plugin` steps (`plugin: mqtt.publish` with its settings under `with`).
