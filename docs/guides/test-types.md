# Test types

Traffic shapes are presets on one engine. Set `load.shape` in a scenario,
pass `--shape` to `stampede run`, choose it in the web UI's new-run dialog,
or type `/run <shape>` in the console. `start` and `max` are rates in rate
mode (`50/s`) and user counts in vus mode. `duration` stretches or shrinks
the preset, except where noted.

| Shape | Question it answers | What it does (defaults) |
|---|---|---|
| `smoke` | Do the journeys work at all? | 2 users (or 1/s) for 1 minute |
| `baseline` | How does it behave at normal traffic? | ramp to `start` over 1m, hold for `duration` (10m) |
| `stress` | What happens past the expected peak? | ramp to `start`, hold 5m, ramp to `max` (2× start), hold 5m, ramp down |
| `spike` | Does it survive a sudden jump? | hold `start`, jump to `max` (10× start) in 10s, hold 3m, drop back, hold 3m |
| `soak` | Do leaks show up over hours? | ramp 2m, hold `start` for `duration` (4h), ramp down |
| `breakpoint` | At what load does it fail? | steps from `start` to `max` (`steps`, default 10, each `stepDuration`, default 2m); stops at the first level that misses a target |
| `steps` | How does each increment change latency? | like breakpoint (5 steps) but never stops early |
| `recovery` | How fast does it return to normal after overload? | normal, 3× overload for 3m, back to normal for 5m |
| `wave` | Does it cope with repeated peaks? | `cycles` (4) of ramp to `max`, hold, ramp to `start`, hold |

Any scenario's journeys work with any shape:

```sh
stampede run checkout.yaml --shape spike --rate 50/s     # start 50/s, max 500/s
stampede run checkout.yaml --shape breakpoint --rate 20/s
```

## Breakpoint runs

A breakpoint run needs at least one target. At the end of each level's hold
period it evaluates the targets over that level only; the first level that
fails ends the run. The report names the last level that held, the first
that failed and which targets failed. Because the search goes past the
targets on purpose, a breakpoint run's verdict is **pass** when at least
one level held.

## The knee

For any run whose load changes, the report groups seconds by planned load,
merges each level's histograms, and draws throughput and p95 against load.
The **knee** is the last level that still scaled: the next one delivered
less than half of the throughput the extra load should have added, tripled
p95 compared with the lightest level, or sharply raised errors.

## Replaying recorded traffic

`mode: replay` sends the requests of a recording at their recorded times,
so the target sees the arrival pattern it saw in production: the morning
ramp, the lunchtime burst, the bots at 3 a.m.

```yaml
target:
  baseURL: ${env.TARGET_URL}
  headers:
    Authorization: Bearer ${secret.API_TOKEN}   # recorded credentials are never replayed
load:
  mode: replay
  replay:
    file: access.log    # common/combined access log, or a HAR file
    speed: 4            # an hour of traffic in 15 minutes
targets:
  - http.p95 < 500ms
```

- **Access logs** give the method, path and query of each request at
  one-second resolution. **HAR files** add request bodies and their
  content type, with millisecond timing; only requests to one host are
  kept (the most frequent, or `host:`).
- Static assets (`.js`, `.css`, images, fonts) are skipped unless
  `static: true`. At most `limit` requests are replayed (default 100,000).
- Headers and cookies from the recording are not replayed. Set any
  credentials the target needs in `target.headers`.
- Every distinct endpoint (`GET /api/products/{id}`: numbers, UUIDs and
  long hex or token segments become placeholders) becomes a journey with
  one step, so the report breaks latency down per endpoint. Leave
  `journeys` out of a replay scenario.
- Replay is an open model. A request is due at its recorded offset divided
  by `speed`, and latency counts from then. If no user is free the request
  is dropped and counted, as in any rate-mode run.
- Distributed runs split the requests between workers, and each request is
  sent once. Like CSV feeders, the file must be readable at the same path
  on the server (under `--data-dir`) and on every worker.

`--rate`, `--vus`, `--duration` and `--shape` do not apply to a replay;
change `speed` instead.

## Targeted stresses

Some stresses are journeys rather than shapes. The [product
packs](packs.md) and ShopLab's scenarios include:

| Stress | Exposes | Where |
|---|---|---|
| Flash-sale spike | capacity for a sudden jump on hot pages | `packs/ecommerce/stresses/flash-sale-spike.yaml` |
| Last-item contention | races such as overselling | `packs/ecommerce/stresses/last-item-contention.yaml` |
| Cold cache spike | everyone hitting an empty cache at once | `examples/shoplab/scenarios/cold-cache-spike.yaml` |
| Login soak | memory growth in the session store | `examples/shoplab/scenarios/login-soak.yaml` |
| Data growth | the same test against more rows | `examples/shoplab/scenarios/order-history.yaml` after reseeding |
| Seat-lock contention | double-sold seats, lock contention across events | `packs/ticketing/stresses/seat-lock-contention.yaml` |
| On-sale rush | a waiting room and everything behind it under a thirtyfold spike | `packs/ticketing/stresses/on-sale-rush.yaml` |
| Login storm | the sign-in ceiling when password hashing is the cost | `packs/identity/stresses/login-storm.yaml` |
| Refresh wave | token refreshes arriving together, wave after wave | `packs/identity/stresses/refresh-wave.yaml` |
| Rate-limit burst | fast 429s with Retry-After for a client over its limit | `packs/public-apis/stresses/rate-limit-burst.yaml` |
| Noisy neighbour or tenant | one client or tenant slowing everyone else | `packs/public-apis/stresses/noisy-neighbour.yaml`, `packs/saas/stresses/noisy-tenant.yaml` |
| Fan-out and reconnect storm | message delivery to a crowded room; every client reconnecting at once | `packs/chat/stresses/fan-out.yaml`, `packs/chat/stresses/reconnect-storm.yaml` |
| Long context, long streams | first-token time behind long prompts; streams held open for minutes | `packs/llm-apps/stresses/long-context.yaml`, `packs/llm-apps/stresses/long-streams.yaml` |
| Telemetry burst, device reconnect storm | ingest falling behind a fleet; sign-in and presence under a reconnect wave | `packs/iot/stresses/telemetry-burst.yaml`, `packs/iot/stresses/reconnect-storm.yaml` |
| Backlog recovery | how large consumer lag grows in a burst and how long it takes to drain | `packs/event-pipelines/stresses/backlog-recovery.yaml` |
| Connection-pool squeeze, write contention | waiting for a database connection; writes queueing on hot rows | `packs/databases/stresses/connection-pool.yaml`, `packs/databases/stresses/write-contention.yaml` |
| Matchmaking rush | time to a match as the queue grows | `packs/gaming/stresses/matchmaking-rush.yaml` |

## Slower networks

`target.network` makes every virtual user's connections behave like a
slower network, inside the generator, with no special privileges:

```yaml
target:
  baseURL: https://staging.example.com
  network: { profile: 3g }                 # or slow-3g, 4g, slow-wifi
  # or explicit values, which override the profile:
  # network: { rtt: 250ms, jitter: 50ms, loss: 1%, down: 1.6mbps, up: 768kbps }
```

| Profile | Round trip | Down | Up |
|---|---|---|---|
| `slow-3g` | 400 ms | 400 kbit/s | 400 kbit/s |
| `3g` | 300 ms | 1.6 Mbit/s | 768 kbit/s |
| `4g` | 70 ms | 12 Mbit/s | 6 Mbit/s |
| `slow-wifi` | 30 ms | 2 Mbit/s | 1 Mbit/s |

The round trip is added once per connection set-up and once per
request/response exchange; `jitter` varies each round trip; bandwidth is
limited per connection. `loss` reproduces what packet loss does to a TCP
connection: a share of transfers stalls for a retransmission timeout
(200 ms, or 1.5 × RTT if longer) before continuing. Measured
latency includes the emulated network, which is the point: it shows what
those users experience. It applies to HTTP, GraphQL, SSE and WebSocket
steps; gRPC steps are not shaped.

## Large payloads

Upload and download limits, request-body buffering and memory per request
show up only with big bodies. A `generate` feeder with a `text(size)`
field makes a body of that size once and reuses it, so the generator
spends its time sending, not building payloads:

```yaml
data:
  upload: { generate: { file: "text(5MB)", name: word } }
journeys:
  - name: upload
    steps:
      - post: /api/files
        form: { name: "${data.upload.name}", content: "${data.upload.file}" }
        check: { status: [201, 413] }   # 413 is the right refusal above your limit
load: { mode: rate, rate: 5/s, duration: 2m }
targets:
  - upload.p95 < 2s
```

`text(size)` takes sizes such as `512`, `64KB` or `5MB`, up to 64 MB per
field. Downloads need nothing special: the report records bytes received
per step, and a step's `timeout` bounds a slow transfer. Fault injection
(`stampede agent`, above) covers failing a dependency mid-run.

**Planned:** connection floods and slow clients.
