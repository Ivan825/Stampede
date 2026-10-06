# Architecture

One Go binary in several roles.

```
 CLI / terminal console / web UI / CI
              │  REST + server-sent events (/api/v1)
              ▼
 ┌──────────────────────── stampede server ────────────────────────┐
 │ API (chi, OpenAPI-generated)   auth, roles, audit                │
 │ run manager ─ executors:  in-process engine  │  coordinator ─────┼── gRPC (:8081) ──▶ stampede worker × N ──▶ target
 │ AI pipeline (optional)        safety: targets, caps, kill switch │
 └──────────────┬──────────────────────────────────────────────────┘
                ▼
     PostgreSQL / TimescaleDB  (runs, per-second metrics, reports, audit)
```

| Package | Role |
|---|---|
| `internal/scenario` | format, parsing, validation, CEL templating, load plans |
| `internal/engine` | virtual users, open/closed executors, feeders, precise dispatch |
| `internal/protocol/*` | HTTP/1.1 and HTTP/2 (with phase timing and a DNS cache), SSE, gRPC; GraphQL and WebSocket live in the engine |
| `internal/metrics` | sparse HDR-style histograms, per-interval snapshots, collector |
| `internal/report` | verdicts, targets, knee, breakpoint, comparison, exports |
| `internal/runner` | in-process and distributed runs producing reports |
| `internal/coordinator`, `internal/worker`, `internal/wire`, `proto/` | distributed execution: registration, clock sync, sharding, merging, loss, saturation |
| `internal/server`, `internal/store`, `internal/auth`, `internal/keyring` | control plane, persistence, identity, secrets |
| `internal/safety` | target classification, ownership verification, caps, host policy |
| `internal/ai` | providers and the generation pipeline |
| `internal/observe` | Prometheus `query_range` for target metrics, trace link templates |
| `internal/notify` | signed webhook, Slack and Discord delivery with retries and an SSRF guard |
| `internal/telemetry` | OpenTelemetry tracing of the server itself (OTLP) |
| `internal/pack`, `packs/` | product packs |
| `internal/cli`, `internal/tui`, `internal/client` | command line and console |
| `web/` | React UI, embedded into the binary |

## A distributed run

1. The API validates the scenario, applies overrides, checks caps and
   stores the run.
2. The coordinator picks workers and splits load in proportion to their
   CPUs: each gets a slice of arrivals and of unique data rows.
3. It measures each worker's clock offset over several round trips and sends
   a start time a few seconds ahead in the worker's own clock; workers start
   within a fraction of a millisecond of each other.
4. Every second each worker sends its histograms and counters, and the
   five slowest requests of each step with their trace IDs, with a
   sequence number; the coordinator merges them per second (a resend is never
   counted twice) and the server stores and streams them.
5. Three missed heartbeats mark a worker lost: its data so far is kept, the
   run continues with less load, and the report marks the window.
6. At the end the server builds the report from the merged snapshots,
   adds target metrics from Prometheus and trace links when the scenario
   asks for them (through the organisation's integrations), stores it with
   the verdict and notifies the organisation's channels.

Server replicas are active-passive through a Postgres advisory lock: one
serves, the others wait as standbys and take over if it goes away.

**Planned:** mutual TLS with a built-in CA, several active replicas sharing
runs, reassigning a lost worker's share.
