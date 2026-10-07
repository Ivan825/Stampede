# Product packs

A pack is a folder of journeys, stresses and default targets for one kind of
product. Packs need no engine changes: adding a product type is adding a
folder.

```sh
stampede pack list                      # all 20 product types, shipped or planned
stampede init --target http://localhost:8090   # detect, install, dry-run
stampede pack install ecommerce --dir stampede
stampede pack test ecommerce --target http://localhost:8090
stampede pack create fintech            # scaffold a new pack folder
```

In the web UI, **Library** lists the packs built into the server. Each
pack shows its variables and its journey and stress files with their
scenario and journey names; a file's YAML can be copied or saved as a
scenario in a project (`GET /api/v1/packs`, `GET /api/v1/packs/{name}`).

## Shipped packs

All twenty packs are shipped. A pack is shipped only when its journeys
and stresses run without errors against a reference app in CI.

| Pack | Protocols | What it tests | Reference app |
|---|---|---|---|
| [`ecommerce`](../../packs/ecommerce) | HTTP | Browse, search, cart and checkout; flash-sale spike; last-item contention | [ShopLab](../../examples/shoplab) (PostgreSQL, Redis), port 8090 |
| [`saas`](../../packs/saas) | HTTP, GraphQL | Sign-in, dashboards over GraphQL with persisted queries, creating and editing records, heavy reports; tenant isolation; noisy tenant; Monday-morning report spike | [SaaSLab](../../examples/packlab/README.md#saaslab), port 8091 |
| [`llm-apps`](../../packs/llm-apps) | SSE, HTTP | Streaming chat with time to first token and tokens per second; concurrency ramp; long prompts stalling short ones; long streams held open | [LLMLab](../../examples/packlab/README.md#llmlab), port 8092 |
| [`chat`](../../packs/chat) | WebSocket, HTTP | Conversations with fan-out to every room member, idle connections, history; a crowded room; 2,000 open connections; reconnect storm | [ChatLab](../../examples/packlab/README.md#chatlab), port 8093 |
| [`ticketing`](../../packs/ticketing) | HTTP, WebSocket | Seat maps, holds and orders; a WebSocket waiting room; on-sale rush; seat-lock contention with an oversell check | [TicketLab](../../examples/packlab/README.md#ticketlab), port 8094 |
| [`identity`](../../packs/identity) | HTTP | OAuth 2.0 / OpenID Connect password, refresh and client-credentials grants, introspection, revocation; login storm; refresh waves | [AuthLab](../../examples/packlab/README.md#authlab), port 8095 |
| [`public-apis`](../../packs/public-apis) | HTTP | API keys, cursor pagination, idempotent creates, webhook deliveries; rate-limit burst (429 with Retry-After); noisy neighbour; webhook burst | [APILab](../../examples/packlab/README.md#apilab), port 8096 |
| [`fintech`](../../packs/fintech) | HTTP | Balances, history, statements, transfers and bill payments with an Idempotency-Key; duplicate payments raced and reconciled; month-end peak | [BankLab](../../examples/packlab/README.md#banklab), port 8097 |
| [`social`](../../packs/social) | HTTP, WebSocket | Feeds, posts, likes, comments, follows, notifications on a WebSocket; viral spike on a celebrity's post; morning feed rush | [SocialLab](../../examples/packlab/README.md#sociallab), port 8098 |
| [`content`](../../packs/content) | HTTP | News pages, RSS and a JSON API behind a cache: conditional requests, social links with tracking parameters; breaking-news spike; cold cache | [NewsLab](../../examples/packlab/README.md#newslab), port 8099 |
| [`streaming`](../../packs/streaming) | HTTP | HLS driven like a player: playback start, playlists, segments at real-time pace, rendition switches, heartbeats; premiere spike; 1,000-viewer live event | [StreamLab](../../examples/packlab/README.md#streamlab), port 8100 |
| [`edtech`](../../packs/edtech) | HTTP, WebSocket | Courses, quizzes, a timed exam with autosave and a live channel for the timer and heartbeats, assignment submissions; everyone starting at 10:00; deadline rush | [ExamLab](../../examples/packlab/README.md#examlab), port 8101 |
| [`government`](../../packs/government) | HTTP | Results by roll number with PDF marksheets, notices, applications with one-time-code sign-in, sections, uploads and receipts; results day; deadline day | [GovLab](../../examples/packlab/README.md#govlab), port 8102 |
| [`delivery`](../../packs/delivery) | HTTP, WebSocket | Nearby cars, prices, rides and food orders matched and tracked live, drivers streaming locations; dinner rush; location flood | [RideLab](../../examples/packlab/README.md#ridelab), port 8103 |
| [`mobile-backends`](../../packs/mobile-backends) | HTTP, GraphQL | Remote config and a minimum version, device registration, delta sync, analytics batches, a GraphQL home screen; a launch over 3G; push storm; offline catch-up | [MobileLab](../../examples/packlab/README.md#mobilelab), port 8104 |
| [`serverless`](../../packs/serverless) | HTTP | Functions with cold starts, warm instances and idle reclaiming; bursts after quiet periods; a function past its concurrency limit (429 with Retry-After) | [EdgeLab](../../examples/packlab/README.md#edgelab), port 8105 |
| [`iot`](../../packs/iot) | MQTT (plugin), HTTP | Connected devices reporting readings, timed to the platform's ack once stored; device shadows; 2,000 connected devices; telemetry burst; reconnect storm | [IoTLab](../../examples/packlab/README.md#iotlab) (in-process Mochi MQTT broker), ports 8110 and 8111 |
| [`event-pipelines`](../../packs/event-pipelines) | Kafka (plugin), HTTP | Orders published and timed until they come out of a consumer group processed; consumer lag; ingest ramp; backlog recovery | [PipelineLab](../../examples/packlab/README.md#pipelinelab) (in-process kfake cluster), ports 8112 and 8113 |
| [`databases`](../../packs/databases) | SQL and Redis (plugins) | A query mix on PostgreSQL and Redis; a shared connection pool under rising load; write contention on hot rows | [DBLab](../../examples/packlab/README.md#dblab) (PostgreSQL you provide, in-process miniredis), ports 8114 and 8115 |
| [`gaming`](../../packs/gaming) | WebSocket, UDP (plugin), HTTP | Sign-in, matchmaking, a match on a UDP game server, scores; matchmaking rush; a full game server; leaderboard flood | [GameLab](../../examples/packlab/README.md#gamelab), ports 8116 and 8117 |

The last four use [protocol plugins](../plugins.md): install the ones a
pack names (`stampede plugin install mqtt`) on every machine that runs
it. Their reference apps listen on a second port for the broker, cluster,
cache or game server, and the pack's variables say how to reach it
(`MQTT_BROKER`, `KAFKA_BROKERS`, `SQL_DSN` and `REDIS_ADDR`; GameLab's
matchmaking hands out its game server's address). These are fakes and a
small lab, not production brokers or clusters.

Each pack's README lists its files and what each one checks. The PackLab
apps are small in-memory Go servers with planted bottlenecks, each with a
fix behind a flag, so a pack can be tried end to end in a minute:

```sh
go run ./examples/packlab -product ticketing        # TicketLab on :8094
stampede init --target http://localhost:8094        # proposes the ticketing pack, installs it, dry-runs it
stampede run stampede/ticketing/stresses/on-sale-rush.yaml -e TARGET_URL=http://localhost:8094 -o before.html
go run ./examples/packlab -product ticketing -fix all   # restart with the bottlenecks fixed, run again, compare
```

How the packs are tested: `examples/packlab/packs_test.go` starts every
PackLab app in-process, checks that `stampede init`'s probe picks the right
pack with a clear margin (the runner-up may score at most half as much),
runs `stampede pack test` on it, and runs every journey and stress under
load for a couple of seconds, failing on any failed request or
iteration. Packs that use plugins run with the plugins built from
`plugins/` in the same checkout. The databases pack needs a PostgreSQL
database (`STAMPEDE_TEST_POSTGRES_DSN`) and is skipped without one. The
CI `packlab` job repeats the detection and the dry run against the
`packlab` binary with the packs exactly as shipped, one job per pack,
installing the pack's plugins first; for databases it starts PostgreSQL
16 as a service container and also runs the in-process checks. The
`packs` job does the same for e-commerce against ShopLab.

A pack that needs more than the target's URL (a broker address, a
database connection string) declares it under `variables`. `stampede
init` then installs the pack and lists them instead of dry-running it;
pass them with `-e`:

```sh
stampede init --target http://localhost:8110 -e MQTT_BROKER=tcp://localhost:8111
stampede pack test iot --target http://localhost:8110 -e MQTT_BROKER=tcp://localhost:8111
```

A database has no HTTP side to probe, so `init` never proposes the
databases pack; install it with `stampede pack install databases`.

## Layout

```
packs/ecommerce/
├── pack.yaml          # name, title, status, protocols, detection hints, reference app
├── journeys/          # everyday journeys (full scenarios)
├── stresses/          # targeted stresses
├── targets.yaml       # suggested targets
├── data/              # feeder files, referenced as ../data/... from scenarios
└── README.md
```

## Detection

`stampede init` fetches the target's home page, response headers, an
OpenAPI document from common paths (`/openapi.yaml`, `/openapi.json`,
`/swagger.json`, `/v3/api-docs`, ...) and an OpenID Connect discovery
document (`/.well-known/openid-configuration`, whose endpoints count as
paths). Each pack's `detect` block scores matches:

```yaml
detect:
  paths: ["/api/products", "/api/cart", "/api/checkout"]   # 3 points each
  openapiTags: [products, cart, checkout]                   # 2 points each
  htmlMeta: ['og:type" content="product']                   # 3 points each
  headers: { X-Powered-By: shopify, X-RateLimit-Limit: "" } # 2 points each; "" only needs the header
```

The best-scoring shipped pack is proposed and confirmed before anything is
installed.

## Writing a pack

1. Run `stampede pack create <name>` (or copy a shipped pack) and edit
   `pack.yaml`. `create` writes the layout above into `./<name>` (`--dir`
   to choose the parent folder): a journey and a spike stress that request
   the target's home page, `targets.yaml` and a CSV in `data/` that the
   journey reads, all valid scenarios from the start, so
   `stampede pack test ./<name> --target <url>` passes against any site
   that answers `GET /` with 200 before you change anything.
2. Write journeys as normal scenarios using `${env.TARGET_URL}`. Keep each
   file's journeys under two minutes for one user, think times included:
   that is the dry run's budget per file.
3. Add a reference app and make `stampede pack test` pass against it. For
   an in-memory app, add a package under `examples/packlab` and register
   it in `examples/packlab/main.go`; `packs_test.go` then tests it. An app
   with a second listener (a broker, a cache, a UDP server) returns a
   `labkit.App` with the variables the pack needs to reach it, and lists
   the plugins its pack uses so the test builds them.
4. Add the directory to the embed list in `packs/embed.go`, set its status to
   `shipped` in `packs/catalog.yaml`, and add it to the CI `packlab` matrix
   (or a job of its own).
