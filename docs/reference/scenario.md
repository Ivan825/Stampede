# Scenario reference

The authoritative definition is the [JSON Schema](../../schema/scenario.schema.json);
`stampede validate file.yaml` checks a file. This page lists every field.

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
| `target.network` | `{profile, rtt, down, up}` | emulate a slower network per user; profiles `slow-3g 3g 4g slow-wifi` |
| `vars` | map | static variables |
| `data.<name>` | feeder | `csv` \| `json` \| `list` \| `range`, `mode`, `onExhausted` |
| `journeys[]` | | `name`, `weight` (default 1), `tags`, `target` (p50/p90/p95/p99/errors), `steps` |
| `load` | | see below |
| `targets[]` | string | `[scope.]metric op value` |

## Load

| Field | Notes |
|---|---|
| `mode` | `vus` (closed) or `rate` (open); `rate:` alone implies `rate` |
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
