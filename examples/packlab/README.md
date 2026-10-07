# PackLab

PackLab is a set of small reference apps, one per product pack, that the
packs are tested against. Each is an in-memory Go server with no database,
broker or other service to start, so every one runs in-process in tests
and as a single binary in CI. (The e-commerce pack's reference app is
[ShopLab](../shoplab), which uses PostgreSQL and Redis.)

| Product | App | Port | Pack |
|---|---|---|---|
| `saas` | [SaaSLab](#saaslab) | 8091 | [saas](../../packs/saas) |
| `llm-apps` | [LLMLab](#llmlab) | 8092 | [llm-apps](../../packs/llm-apps) |
| `chat` | [ChatLab](#chatlab) | 8093 | [chat](../../packs/chat) |
| `ticketing` | [TicketLab](#ticketlab) | 8094 | [ticketing](../../packs/ticketing) |
| `identity` | [AuthLab](#authlab) | 8095 | [identity](../../packs/identity) |
| `public-apis` | [APILab](#apilab) | 8096 | [public-apis](../../packs/public-apis) |
| `fintech` | [BankLab](#banklab) | 8097 | [fintech](../../packs/fintech) |
| `social` | [SocialLab](#sociallab) | 8098 | [social](../../packs/social) |
| `content` | [NewsLab](#newslab) | 8099 | [content](../../packs/content) |
| `streaming` | [StreamLab](#streamlab) | 8100 | [streaming](../../packs/streaming) |
| `edtech` | [ExamLab](#examlab) | 8101 | [edtech](../../packs/edtech) |
| `government` | [GovLab](#govlab) | 8102 | [government](../../packs/government) |
| `delivery` | [RideLab](#ridelab) | 8103 | [delivery](../../packs/delivery) |
| `mobile-backends` | [MobileLab](#mobilelab) | 8104 | [mobile-backends](../../packs/mobile-backends) |

```sh
go run ./examples/packlab -product ticketing                # on :8094
go run ./examples/packlab -product ticketing -addr :9000
go run ./examples/packlab -product ticketing -fix lock      # switch one bottleneck off
go run ./examples/packlab -product ticketing -fix all       # or PACKLAB_FIX=all
```

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

The `packs` job in CI does step 2 again with the unmodified pack against
the `packlab` binary, and step 1 through `stampede init`. A pack is marked
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

## BankLab

A retail bank. Customers `c0001` ... `c1000` (password `banklab-pass`)
each have a current account (`acc_0001_cur`) and a savings account
(`acc_0001_sav`) with three months of history. `POST /api/login` returns a
bearer token; then accounts, transactions, monthly statements, `POST
/api/transfers` and `POST /api/payments` (to the bank's own billers; no
payment provider is involved), both requiring an `Idempotency-Key`.
Amounts are in cents. `GET /api/ledger/check` reconciles: every balance
adds up to zero across the bank and no key moved money twice. Each
transfer runs a fraud check that takes 5ms.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `lock` | Every transfer and payment holds one ledger-wide lock, fraud check included, so money moves one transfer at a time and balance reads queue behind it | `month-end-peak.yaml`: `payment` and `accounts` p95 as arrivals climb |
| `balance` | A balance is the sum of the account's whole history, recomputed on every read and every transfer | `banking-mix.yaml`: `accounts` and `transfer`, slower as postings pile up |
| `statements` | A monthly statement scans the whole bank's journal (about 100,000 postings) for one account's lines | `month-end-peak.yaml`: `statement` p95 |

## SocialLab

A social network with 5,000 accounts (`user0001` ... `user5000`,
password `sociallab-pass`) and a week of posts. The 20 celebrities
(`user0001` ... `user0020`) post a lot and are followed by everyone; other
accounts follow 50 to 400 others. `POST /api/login` returns a bearer
token; then the home feed, posts, likes, comments, profiles, follows,
trending posts and notifications, live on the WebSocket
`/api/notifications/ws`. Writing a like or a notification costs a 2ms
database round trip.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `timeline` | The home feed gathers every post of every followed account (several thousand) and sorts them all to show twenty | `feed-rush.yaml`: `feed` p95 and CPU as the rate climbs |
| `likes` | Every like takes one global lock, scans the post's likers for a repeat and writes the like row before letting go, so likes run one at a time across the app | `viral-spike.yaml`: `like` p95 |
| `notify` | Notifications are stored and written to the recipient's sockets inside the like, comment or follow request, under one lock | `viral-spike.yaml`: `like` and `comment` p95 |

## NewsLab

A news site with 20,000 articles (`story-00001` ... `story-20000`) in
eight sections, behind a page cache like a CDN's: `Cache-Control`
(`s-maxage` 60 seconds for fronts and feeds, 5 minutes for articles),
`ETag`, `Age`, `X-Cache: HIT|MISS` and 304s for `If-None-Match`. Pages
are HTML (`/`, `/section/{name}`, `/articles/{slug}`), plus `/feed.xml`
and a JSON API. The origin renders four pages at a time and a render
costs 40ms (article) to 100ms (home page). The newsroom publishes with
`POST /api/articles` and the bearer token `newslab-editor`;
`GET /api/cache/stats` reports hits, misses and renders.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `cachekey` | The cache key is the full URL, so links with tracking parameters (`utm_*`, `fbclid`) never hit and every such reader waits for the origin | `breaking-news.yaml`: `social-reader` `article` p95 (seconds, against under a millisecond with the fix) |
| `stampede` | A page that is not cached is rendered by every request that asks for it, so an expired or purged home page sends a crowd to the four render slots | `breaking-news.yaml` and `cold-cache.yaml`: `home` p99 |
| `purge` | Publishing an article empties the whole cache instead of the pages it appears on | `breaking-news.yaml`: `top story` p99 after each update |

## StreamLab

A video service that delivers HLS: 30 on-demand videos (`v001` ...
`v030`, ten minutes each) and two live channels (`live1`, `live2`, on
air since an hour before start-up) in four renditions, 240p to 1080p.
`POST /api/playback` returns a manifest URL and a signed token, which
every playlist and segment request must carry. Segments are two-second
MPEG-TS files at the rendition's real bitrate (100 KB at 240p to 1.5 MB
at 1080p); live playlists list the newest six. Starting playback calls
three backends (entitlement, DRM license, CDN choice) of 40ms each.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `package` | Every segment request packages the segment again, so a thousand live viewers wanting the newest segment do the same work a thousand times | `live-event.yaml`: `newest segment` p95 and CPU |
| `playlist` | Every live playlist request lists every segment since the event began (thousands, growing by one every two seconds) to find the newest six | `live-event.yaml`: `live playlist` p95, worse the longer the event runs |
| `startup` | Starting playback calls its three backends one after another (120ms) instead of together (40ms) | `premiere-spike.yaml` and `vod-mix.yaml`: `play` p95 |

## ExamLab

A learning platform. Students `s00001` ... `s05000` (password
`examlab-pass`), ten courses (`C101` ...), each with a 40-question,
60-minute exam (`EX-101` ...) drawn from a 2,000-question bank and an
essay assignment (`A-101` ...) due an hour after start-up. Answers are
saved one at a time; the live channel `/api/exams/{id}/live?attempt=`
sends the timer every 30 seconds, acknowledges heartbeats and relays
announcements staff post with the bearer token `examlab-staff`.
Submissions are fingerprinted (winnowed 8-grams) and scored for
similarity. Writing an attempt costs 5ms.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `start` | Starting an attempt shuffles the question bank and writes the attempt while holding the exam's one lock, so at 10:00 students start one at a time (at most 200 a second) | `exam-start.yaml`: `start attempt` p95 (seconds, against a few milliseconds with the fix) |
| `autosave` | Every saved answer goes into one log for all attempts, and each save and status read scans the whole log (200ns a row) to find the attempt's answers | `exam-start.yaml` and `take-exam.yaml`: `save answer` p95, rising as answers pile up |
| `similarity` | Each submission is compared with every earlier one under the assignment's lock, so the rush slows down submission by submission and blocks reading the assignment | `deadline-rush.yaml`: `submit work` and `assignment` p95 |

## GovLab

A government portal. Results for 100,000 candidates (roll numbers
`26000001` ... `26100000`; dates of birth from 2008 to 2010, sampled in the pack's
`data/candidates.csv`), PDF marksheets, notices, and applications for a
scholarship that closes 48 hours after start-up: sign in with a one-time
code (`POST /api/otp/request`; test mode returns the code as
`testCode`), save four sections, upload documents, submit for an
acknowledgement number and a PDF receipt. Every query uses one of ten
database connections.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `index` | Results are found by scanning all 100,000 rows (20ms) on a database connection, so on results day the ten connections run out and notices queue too | `results-day.yaml`: `lookup`, `marksheet` and `notices` p95 |
| `pdf` | Every marksheet and receipt compresses the 400 KB letterhead image again | `results-day.yaml`: `marksheet` p95 and CPU |
| `lock` | Every application save takes one portal-wide lock and writes the application and an audit entry (2ms) before letting go, so saves run one at a time | `deadline-day.yaml`: `save section` and `submit` p95 as arrivals climb |

## RideLab

Rides and food delivery in a 15 km square city. 10,000 simulated
drivers (`d00001` ... `d10000`) circle their neighbourhoods; riders are
`r0001` ... `r5000`; everyone's password is `ridelab-pass`; 300
restaurants. A trip is matched half a second after it is requested, to
the free driver with the shortest ETA among the five closest; trips run
thirty times faster than real time. ETAs come from a routing service
that takes 5ms a call plus a shortest-path search over 22,500 road
blocks. Drivers who stream locations on `/api/drivers/stream` appear on
the map where their app says.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `dispatch` | Matching calls the routing service for each candidate while holding the fleet lock, so trips are matched one at a time (at most about 30 a second) and the map and location updates wait | `dinner-rush.yaml`: `matched` and `nearby cars` p95 (seconds, against the half-second dispatch delay with the fix) |
| `geo` | Finding nearby drivers, for the map and for matching, checks the distance to all 10,000 under the fleet lock | `location-flood.yaml`: `nearby cars`, `cancel` and `location ack` p95 |
| `eta` | Every ETA is a routing call: each price check, each match, and each tracking update to each rider, every second; nothing is cached | `rider-mix.yaml`: CPU, and a routing call a second for every rider following a trip |

## MobileLab

The backend of a mobile to-do and habits app. Users `u00001` ...
`u20000` (password `mobilelab-pass`) have 20 to 1,000 items each. Every
API request needs `X-App-Version` of at least 3.0.0 (older versions get
426 with `minVersion`); remote config at `/api/v1/config` evaluates 300
feature flags for the platform, version and rollout bucket; `/api/v1/sync`
takes offline changes and returns changes since a `v<number>` token
(the counter starts at 1000); analytics come in batches; the home screen
is GraphQL at `/api/v1/graphql` with automatic persisted queries.

| Fix | Bottleneck | Where it shows |
|---|---|---|
| `config` | Every config request parses the whole 700 KB config document (300 flags with targeting rules) and evaluates it, and forbids caching | `push-storm.yaml`: `config` p95, and everything else as the CPU runs out |
| `sync` | Delta sync ignores the token and sends every item the user has, every launch | `slow-network.yaml`: `delta sync` (about 570ms against 310ms with the fix on 3G); `push-storm.yaml`: `delta sync` |
| `events` | Each analytics event is written on its own (200µs) under one global lock | `offline-catchup.yaml` and `push-storm.yaml`: `events` p95 |
