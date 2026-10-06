# PackLab

PackLab is a set of small reference apps, one per product pack, that the
packs are tested against. Each is an in-memory Go server with no broker or
other service to start, so every one runs in-process in tests and as a
single binary in CI. The apps for packs that use protocol plugins embed
what they need in-process (a Mochi MQTT broker, a kfake Kafka cluster,
miniredis, a UDP server) and listen on a second port for it; DBLab also
needs a PostgreSQL database to seed. (The e-commerce pack's reference app
is [ShopLab](../shoplab), which uses PostgreSQL and Redis.)

| Product | App | Port | Second port | Pack |
|---|---|---|---|---|
| `saas` | [SaaSLab](#saaslab) | 8091 | | [saas](../../packs/saas) |
| `llm-apps` | [LLMLab](#llmlab) | 8092 | | [llm-apps](../../packs/llm-apps) |
| `chat` | [ChatLab](#chatlab) | 8093 | | [chat](../../packs/chat) |
| `ticketing` | [TicketLab](#ticketlab) | 8094 | | [ticketing](../../packs/ticketing) |
| `identity` | [AuthLab](#authlab) | 8095 | | [identity](../../packs/identity) |
| `public-apis` | [APILab](#apilab) | 8096 | | [public-apis](../../packs/public-apis) |
| `iot` | [IoTLab](#iotlab) | 8110 | 8111 MQTT | [iot](../../packs/iot) |
| `event-pipelines` | [PipelineLab](#pipelinelab) | 8112 | 8113 Kafka | [event-pipelines](../../packs/event-pipelines) |
| `databases` | [DBLab](#dblab) | 8114 | 8115 Redis | [databases](../../packs/databases) |
| `gaming` | [GameLab](#gamelab) | 8116 | 8117 UDP | [gaming](../../packs/gaming) |

```sh
go run ./examples/packlab -product ticketing                # on :8094
go run ./examples/packlab -product ticketing -addr :9000
go run ./examples/packlab -product ticketing -fix lock      # switch one bottleneck off
go run ./examples/packlab -product ticketing -fix all       # or PACKLAB_FIX=all
go run ./examples/packlab -product iot -listen :1883        # MQTT on another port
go run ./examples/packlab -product databases -postgres postgres://localhost:5432/lab   # or PACKLAB_POSTGRES_DSN
```

An app with a second port logs the variables its pack needs at start,
such as `MQTT_BROKER=tcp://localhost:8111`.

`-fast` shortens deliberate costs (token pacing, delivery and admission
ticks, simulated database round trips, password-hashing rounds) so tests
finish quickly; the bottlenecks stay in place.

Each app has planted bottlenecks, and each has a fix behind a `-fix` name.
The demo is the same as ShopLab's: run the pack's stress, find the
bottleneck in the report, restart with the fix, run it again and compare
(`stampede compare`). Every bottleneck and its fix is checked by the app's
own tests (`go test ./examples/packlab/...`), mostly by counting the work
done or the most requests in a critical section at once, not by timing.

## How the packs are tested

`packs_test.go` starts every app in-process and, for its pack:

1. probes it as `stampede init` does and checks the right pack scores
   highest (and that `init` proposes it);
2. runs `stampede pack test` on the pack (think times cut to 10ms), which
   dry-runs every journey of every file once;
3. runs every journey and stress file through the real runner for a
   couple of seconds (rates and user counts capped, think times cut to a
   tenth), and fails on any failed request or iteration. Stresses whose
   point is a refusal (the rate-limit burst, the seat race) must also
   provoke it.

For packs that use plugins it first builds them from `../../plugins` into
a temporary plugin directory, and passes the app's variables to both
runs. The databases pack runs only when `STAMPEDE_TEST_POSTGRES_DSN`
names a PostgreSQL database, and has no step 1: a database has nothing
to probe over HTTP.

The `packlab` job in CI does step 2 again with the unmodified pack against
the `packlab` binary, and step 1 through `stampede init`; for databases it
starts PostgreSQL 16 as a service container and runs the in-process
checks as well. A pack is marked
shipped in [`packs/catalog.yaml`](../../packs/catalog.yaml) only when all
of this passes.

## SaaSLab

A multi-tenant B2B app. Twenty tenants with 25 users each
(`user01@acme.test` ... `user25@krusty.test`, password `saaslab-pass`);
`megacorp` has 100,000 records and the others 1,000. REST under `/api`
(login, records, reports) and GraphQL at `/graphql` (dashboard, records,
reports, mutations; automatic persisted queries). Data is in memory behind
a simulated database where every query costs a 300µs round trip.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `reports` | Reports scan the tenant's records and every tenant shares two report workers, so a big tenant's reports make everyone else's wait | `noisy-tenant.yaml`: `everyone-else` p95; `report-spike.yaml` |
| `n1` | GraphQL loads each record's owner with its own query: a page of 20 costs 22 round trips | the `dashboard` step in `saas-mix.yaml` |
| `count` | Every record list counts all matching records to report a total, so lists slow down with tenant size | `noisy-tenant.yaml`: the `list` step |

## LLMLab

An OpenAI-compatible API: `POST /v1/chat/completions` (streaming with
`stream: true`, OpenAI chunk format, `data: [DONE]` at the end),
`/v1/completions`, `/v1/embeddings`, `/v1/models`. No key needed. Tokens
are made up and paced at 10ms each (100 tokens a second); prompt
processing costs 20µs per character.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `batch` | Only four generations run at once; the rest queue before their first token | `concurrency-ramp.yaml`: first-token p95 climbs with each step |
| `chunked` | Prompt processing holds one global lock, so a long prompt delays everyone's first token | `long-context.yaml`: `short-chat` first-token time |
| `usage` | Usage accounting re-tokenises the whole reply after every token (CPU grows with the square of the length) | `long-streams.yaml`: CPU, and events per second |

## ChatLab

Chat rooms over WebSocket. `POST /api/login {"user": name}` returns a
token; the socket is `/ws?room=<name>` with `Authorization: Bearer`.
Rooms: general, random, support, engineering, sales, town-hall. Messages
are JSON with a `type`: send `say`, `typing`, `history`, `ping`; receive
`welcome`, `message`, `ack` (after a `say` has gone to every member),
`typing`, `history`, `presence`, `pong`.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `fanout` | A message is written to each member in turn while the room lock is held, so delivery time grows with the room and one slow reader stalls it | `fan-out.yaml`: `ack` p95 as the room fills |
| `history` | The room log is never trimmed and a history request encodes all of it to return the tail | `reconnect-storm.yaml`: `history` step, worse every run |
| `presence` | Each join and leave sends the full member list to every member (the square of the room size) | `reconnect-storm.yaml`, `fan-out.yaml` while users join |

## TicketLab

Twenty events. Event 1 is the headline on-sale (5,000 seats, behind a
waiting room); event 2 is a 200-seat club show used for the seat race;
events 3 to 20 have 2,000 seats. Sessions are a `sid` cookie. Holds last
two minutes; `POST /api/orders` buys what the session holds. The waiting
room admits 50 people a second.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `lock` | One lock guards every event's seats, so the on-sale rush blocks holds for every other event | `on-sale-rush.yaml`: `other-events` hold p95 |
| `seatmap` | Holds are never pruned; the seat map and the expiry sweep replay every hold ever made | `on-sale-rush.yaml` and `browse-and-book.yaml`: `seat map`, worse as the run goes on |
| `queue` | Each waiting connection scans the whole queue every tick to find its place | `on-sale-rush.yaml`: CPU and admission pace as the queue grows |

## AuthLab

An OAuth 2.0 and OpenID Connect token service with discovery at
`/.well-known/openid-configuration`, form-encoded `POST /oauth/token`
(`password`, `refresh_token` with rotation and reuse detection,
`client_credentials`), `/userinfo`, `/oauth/introspect`, `/oauth/revoke`
and Ed25519-signed JWT access tokens. Users `user0001@authlab.test` ...
`user1000@authlab.test`, password `authlab-pass`; clients `web` and
`mobile` (public) and `reporting-service` (secret `authlab-secret`).
Passwords are hashed with PBKDF2-SHA256, 20,000 rounds (a few
milliseconds each, on purpose).

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `lock` | Password hashes are checked while holding the store's only lock, so sign-ins run one at a time and refreshes queue behind them | `login-storm.yaml`: `password grant` p95 and the sign-in ceiling |
| `index` | Refresh tokens sit in a list that is never pruned and is scanned on every refresh | `refresh-wave.yaml`: `refresh` p95 rises wave after wave |

## APILab

A public API. Keys go in `X-API-Key`: `sk_test_00001` ... `sk_test_20000`
on the standard plan (20 requests a second) and `sk_test_free` on the
free plan (5 a second). Over the limit, the answer is 429 with
`Retry-After` and `X-RateLimit-*` headers. `/v1/items` (cursor
pagination; creates take `Idempotency-Key`), `/v1/usage`, `POST
/v1/webhooks/test` and `GET /v1/deliveries/{id}`; deliveries go to a
simulated receiver that takes 20ms each.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `keys` | A key is found by scanning all 20,001 key hashes | every request; `api-mix.yaml` p95 |
| `limiter` | Rate limits are a sliding-window log behind one global lock that also logs refused attempts, so a client that retries without backing off never gets through, and every key waits on the one lock | `noisy-neighbour.yaml`: the `noisy-client` journey's status counts (5 of 1,680 requests got a 200 in a 15-second run); with heavier floods, `well-behaved` p95 |
| `webhooks` | One worker delivers every webhook, so a burst of events queues up | `webhook-burst.yaml`: the `webhook` journey's duration and polls |

## IoTLab

An IoT platform. An in-process [Mochi MQTT](https://github.com/mochi-mqtt/server)
broker on the second port accepts devices `dev-0` ... `dev-19999` (client
id and username the device id, password `iotlab-pass`); each may only use
topics under `devices/<id>/`. Readings published to
`devices/<id>/telemetry` (JSON with a `seq`) are stored by an ingest
service, which acknowledges each on `devices/<id>/acks` with the same
`seq`. A device asks for its desired state on `devices/<id>/shadow/get`
(`{"token": ...}`) and gets it on `devices/<id>/shadow`; `PUT
/api/devices/{id}/shadow` sets it and pushes it to a connected device.
HTTP: `/api/fleet`, `/api/devices`, `/api/devices/{id}`,
`/api/devices/{id}/telemetry`, `/api/ingest`. Storing a reading costs
1ms.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `ingest` | One worker stores every reading in turn, so a busy fleet's readings queue and acks fall behind | `telemetry-burst.yaml`: `stored` p95 climbs while `report` stays quick |
| `presence` | Every connect and disconnect recounts the whole fleet's online summary while holding the registry lock that sign-ins also take | `reconnect-storm.yaml`: `connect` p99 |

## PipelineLab

An event pipeline on an in-process Kafka cluster (franz-go's
[kfake](https://pkg.go.dev/github.com/twmb/franz-go/pkg/kfake)) on the
second port, with topics `orders` and `orders.enriched` (6 partitions
each). The `enricher` consumer group reads each order (JSON with a
`customer` id from 1 to 10000), looks the customer up (4ms) and writes the
order with the customer added to `orders.enriched` with the same key,
partition and timestamp. HTTP: `/api/topics` (log-end offsets),
`/api/consumer-groups`, `/api/consumer-groups/enricher/lag` (committed
offset against log end, per partition), `/api/pipeline`.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `parallel` | Every record of every partition is enriched one at a time, so throughput is one lookup at a time | `ingest-ramp.yaml` and `backlog-recovery.yaml`: the tracers' `enriched` age, and lag |
| `commit` | The enricher flushes and commits its offset after every record | the same, a smaller share; `/api/pipeline` counts commits |

## DBLab

The data tier of a shop. On start it drops and recreates a `dblab` schema
in the PostgreSQL database given with `-postgres` (or
`PACKLAB_POSTGRES_DSN`): `customers` (20,000), `products` (1,000, each
with a million in stock) and `orders` (200,000 over the last 30 days),
plus `order_totals`, which a trigger updates for every new order. Point
it at a database it may create a schema in, not one you care about. An
in-process Redis ([miniredis](https://github.com/alicebob/miniredis)) on
the second port holds a hash `product:<id>` per product and a sorted set
`popular`. The `SQL_DSN` it logs carries `search_path=dblab`, so the
pack's SQL names tables without the schema. HTTP: `/healthz`,
`/api/stats`.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `index` | `orders` has no index on `customer_id`, so a customer's order history scans the table | `query-mix.yaml` and `connection-pool.yaml`: `order history` |
| `hotrow` | The trigger adds every order to one totals row, so concurrent inserts wait for each other's commit | `write-contention.yaml`: `place order` p95 as users rise |

## GameLab

A game backend. `POST /api/login {"player": name}` returns a token (the
player's rating, 1000 to 1999, comes from the name). The matchmaking
socket is `/api/matchmaking` with `Authorization: Bearer`: send
`{"type": "queue", "mode": "duel"}` (2 players) or `"squad"` (4), receive
`queued`, then `match` with the match id, the game server's `host:port`
and a ticket. Players are matched within a rating window that widens the
longer they wait; after 3 seconds the rest of the match is filled with
bots. The UDP game server on the second port speaks text datagrams:
`join <match> <ticket>`, `input <match> <seq> ...` (answered on the next
20-a-second tick), `leave <match>`, `ping`. The leaderboard starts with
100,000 players (`p1` ... `p100000`): `GET /api/leaderboard`,
`/api/leaderboard/{player}`, `POST /api/scores` (the best score counts).

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `matchmaking` | Every tick, each waiting player is compared with every other to find the closest ratings, under the queue lock joins also take | `matchmaking-rush.yaml`: `matched` and `queued` as the queue grows |
| `leaderboard` | Every score re-sorts the whole leaderboard, and every rank lookup scans it | `leaderboard-flood.yaml`: `submit score` and `my rank` |
