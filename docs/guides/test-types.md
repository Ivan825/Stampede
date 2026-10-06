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

## Targeted stresses

Some stresses are journeys rather than shapes. The [e-commerce
pack](packs.md) and ShopLab's scenarios include:

| Stress | Exposes | Where |
|---|---|---|
| Flash-sale spike | capacity for a sudden jump on hot pages | `packs/ecommerce/stresses/flash-sale-spike.yaml` |
| Last-item contention | races such as overselling | `packs/ecommerce/stresses/last-item-contention.yaml` |
| Cold cache spike | everyone hitting an empty cache at once | `examples/shoplab/scenarios/cold-cache-spike.yaml` |
| Login soak | memory growth in the session store | `examples/shoplab/scenarios/login-soak.yaml` |
| Data growth | the same test against more rows | `examples/shoplab/scenarios/order-history.yaml` after reseeding |

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

**Planned:** connection floods, slow clients, large payloads, and failing a dependency
mid-run through an in-environment agent.
