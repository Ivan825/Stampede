# Architecture

One Go binary in several roles.

```
 CLI / terminal console / CI / web UI (read only)
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
5. Three missed heartbeats mark a worker lost and its data so far is kept.
   If an idle worker is connected, it takes over the lost worker's exact
   share from the next interval boundary at least two seconds away: it
   synchronises its clock, starts part-way through the plan (arrivals
   before that point are skipped, so nothing is sent twice) and reports as
   a new member. Otherwise the run continues with less load. Either way
   the report marks the window in which the share was missing. Runs with
   a fixed iteration count, or with `unique` table data whose progress the
   lost worker took with it, are never handed over.
6. At the end the server builds the report from the merged snapshots,
   adds target metrics from Prometheus and trace links when the scenario
   asks for them (through the organisation's integrations), stores it with
   the verdict and notifies the organisation's channels. If the database
   is unreachable then, the report (built in memory) and the final status
   are retried for up to two minutes; per-second metrics written during
   an outage are lost from the stored timeline but not from the report.

Server replicas are active-passive by default (`--ha standby`): one holds
a Postgres advisory lock and serves, the others wait and take over if it
goes away. With `--ha active` every replica serves:

- each run belongs to the replica that started it (`runs.owner_replica`);
- replicas heartbeat into a `replicas` table, and a replica silent for 30
  seconds has its unfinished runs and AI jobs marked failed by the others;
- stop and kill requests reach the owning replica through Postgres
  `LISTEN/NOTIFY`, and other replicas serve the live view by polling the
  run's stored points;
- workers connect to every replica (`--server a,b,c` or `--server
  dns:host:port`) and run load for one at a time; their heartbeat tells
  the other replicas they are busy.

[Scheduled runs](guides/schedules.md) are claimed with one conditional
update per firing, so a firing starts one run however many replicas look.

## Worker security

With `stampede server --worker-mtls` (the default in Docker Compose and the
Helm chart), the worker port uses mutual TLS with a CA built into the
server:

1. The server derives an Ed25519 CA from its master key (HKDF), so every
   replica has the same CA without storing it, and logs its fingerprint.
2. A worker started with `--mtls` makes a fresh key pair and opens a
   TLS 1.3 connection. It proves it knows the join token by sending an
   HMAC, keyed by the token, over keying material exported from that TLS
   session. The token itself is never sent, and the proof is useless on
   any other connection, so a machine in the middle can neither learn the
   token nor relay the proof.
3. The server answers with a certificate for the worker's public key
   (valid 24 hours, for client authentication only), the CA certificate,
   and its own HMAC over both. The worker trusts the CA only if that HMAC
   checks out, or if it matches `--ca-fingerprint` when one is given.
4. The worker then connects with its certificate. The server requires it
   on the run stream, and the worker checks the server's certificate
   against the pinned CA and for server-only usage, so a worker
   certificate can never pose as the server.
5. Workers renew at half-life. Rotating the join token locks every worker
   out within a day.

Without `--worker-mtls`, the worker port can use a certificate from your
own CA (`--worker-tls-cert`, `--worker-tls-key`, workers `--ca`) or no TLS
on a trusted network (`--insecure`). In both cases the join token travels
in `Hello`.

**Planned:** handing a run to another server replica when its owner dies.
A spare takes over a lost worker's share only when it is idle; shares are
not redistributed across workers that are already busy with the run.
