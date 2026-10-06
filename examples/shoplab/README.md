# ShopLab

ShopLab is the sample app that ships with Stampede: a small e-commerce JSON API
written in Go (chi, pgx, go-redis, Prometheus). It has **six real performance
bottlenecks planted on purpose**, and each one has a fix behind an environment
flag. The demo goes like this:

1. Run a Stampede test against ShopLab.
2. Find the bottleneck in the results and in ShopLab's `/metrics`.
3. Flip the matching `SHOPLAB_FIX_*` flag and restart.
4. Rerun the same test and compare the two runs.

The bottlenecks are real. They show up as round trips, table scans, pool
queueing, repeated expensive queries, lost updates and retained heap, and
none of them is a `sleep`. Every one is checked by the integration tests in
[`integration/`](integration/).

## Quick start (Docker)

```sh
cd examples/shoplab
docker compose up --build -d     # postgres + redis + one-shot seed + API on :8090
curl localhost:8090/readyz
curl 'localhost:8090/api/products?q=shoe&per_page=3'
```

The first start seeds 10,000 products, 1,000 users, 20,000 orders and about
240,000 reviews, which takes about 10 s. Later starts skip seeding.

To turn fixes on, recreate the API container with the flags you want:

```sh
SHOPLAB_FIX_N1=1 docker compose up -d shoplab     # one fix
SHOPLAB_FIX_ALL=1 docker compose up -d shoplab    # every fix
docker compose up -d shoplab                      # back to all bottlenecks
```

To make the missing-index bottleneck hurt more, reseed with more order history:

```sh
docker compose run --rm seed seed --reset --orders-per-user 200   # 200,000 orders
docker compose up -d --force-recreate shoplab
```

Postgres is published on `127.0.0.1:55432` and Redis on `127.0.0.1:56379`
(change them with `SHOPLAB_PG_PORT` / `SHOPLAB_REDIS_PORT`). The API port is
set by `SHOPLAB_PORT`, which defaults to 8090.

## Running locally (without the image)

```sh
docker compose up -d postgres redis
export DATABASE_URL='postgres://shoplab:shoplab@127.0.0.1:55432/shoplab?sslmode=disable'
export REDIS_URL='redis://127.0.0.1:56379/0'
go run ./cmd/shoplab seed            # --products 10000 --users 1000 --orders-per-user 20
SHOPLAB_ADMIN=1 go run ./cmd/shoplab # same as `shoplab serve`
```

Subcommands: `serve` (the default), `seed`, `migrate`, `users-csv`,
`healthcheck` and `version`. Migrations are embedded SQL files. They run at
startup, are recorded in `schema_migrations` and take a Postgres advisory
lock, so several replicas can start at the same time safely.

### Seed data

- The seed is deterministic: the RNG seed is fixed and timestamps are anchored
  to 2026-09-01. Running the same flags twice gives the same data. Password
  salts are the only exception.
- Users `user0001@shoplab.test` … `user1000@shoplab.test` all have the
  password `shoplab-pass`. [`data/users.csv`](data/users.csv) (`email,password`)
  is ready to use as a Stampede data feeder. Regenerate it with
  `go run ./cmd/shoplab users-csv --users 1000 > data/users.csv`.
- Passwords are hashed with **bcrypt cost 4** (the minimum), so a login costs
  about 1 ms and login is never the bottleneck under test. Do not copy this
  setting into a real system; use cost 10–12 or higher there.
- **Products 1–10 have stock 5** and are left out of the seeded order
  history, so the oversell race has a clean starting point.
- **Products 1–100 are "popular"** (the first 1% of ids, at least 10). Each has
  2,000 reviews, which makes their review summary expensive to compute. All
  other products have 0–8 reviews.
- Seed flags: `--products`, `--users`, `--orders-per-user`, `--hot-reviews`,
  `--reset` (truncate first) and `--if-empty` (do nothing if already seeded).

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `SHOPLAB_ADDR` | `:8090` | Listen address |
| `DATABASE_URL` | `postgres://shoplab:shoplab@localhost:5432/shoplab?sslmode=disable` | Postgres |
| `REDIS_URL` | `redis://localhost:6379/0` | Redis |
| `SHOPLAB_ADMIN` | off | Enables `POST /admin/cache/flush` and `POST /admin/stock/reset` |
| `SHOPLAB_CACHE_TTL` | `10s` | Product-detail cache TTL |
| `SHOPLAB_SESSION_TTL` | `15m` | Session lifetime (use `1m` for a short soak test) |
| `SHOPLAB_LOG_LEVEL` | `info` | `debug` adds one JSON access-log line per request |
| `SHOPLAB_FIX_N1` … `SHOPLAB_FIX_LEAK` | off | One flag per bottleneck (see below) |
| `SHOPLAB_FIX_ALL` | off | Turns every fix on |

A flag counts as on when it is `1`, `true`, `yes` or `on`. `GET /` and the
`shoplab_fix_enabled{fix}` gauge both report which fixes are active, so runs
can be labelled.

## The six bottlenecks

| # | Flag | Bottleneck | Endpoint | Stampede test that finds it | What to look at |
|---|---|---|---|---|---|
| 1 | `SHOPLAB_FIX_N1` | **N+1 queries.** The listing fetches a page, then runs one query per product for its category and another for its rating: 42 statements for 20 items. | `GET /api/products` | Load test on browsing at a steady arrival rate | p95 latency, throughput, `rate(shoplab_db_queries_total)` ÷ request rate (≈ 42 vs 2) |
| 2 | `SHOPLAB_FIX_INDEX` | **Missing index.** `orders` has no `(user_id, created_at)` index, so every order-history call scans the whole table, twice. It gets worse as data grows. When the flag is on, startup runs `CREATE INDEX IF NOT EXISTS`; when it is off, startup drops the index, so the toggle works on the same database. | `GET /api/orders` | Load test with logged-in users (`data/users.csv`), ideally after `--orders-per-user 200` | p50/p95 of `/api/orders`, Postgres CPU |
| 3 | `SHOPLAB_FIX_POOL` | **Undersized connection pool.** `pgxpool` `MaxConns` is 5 when unfixed and 40 when fixed. Requests queue for a connection while Postgres sits idle. | everything that touches the DB | Stress or ramp test that raises concurrency until latency bends | `shoplab_db_pool_wait_total`, `shoplab_db_pool_wait_duration_seconds_total`, `acquired_conns` stuck at `max_conns` |
| 4 | `SHOPLAB_FIX_CACHE` | **Cache stampede.** Product detail is cached in Redis with a fixed 10 s TTL. On a miss, every concurrent request recomputes the expensive review summary, and keys filled together expire together. The fix adds per-key singleflight, ±20% TTL jitter and stale-while-revalidate. | `GET /api/products/{id}` | Spike test on popular products (ids 1–100) right after `POST /admin/cache/flush` | max/p99 latency, `shoplab_cache_loads_total`, the `X-Cache` header |
| 5 | `SHOPLAB_FIX_RACE` | **Oversell race.** Checkout reads stock without a lock, checks it in Go, inserts the order, then writes back `stock = <value computed from the stale read>`. Concurrent buyers all succeed and updates are lost. The fix is `UPDATE … SET stock = stock - $n WHERE id = $id AND stock >= $n` inside the transaction. | `POST /api/checkout` | Concurrency/spike test where many VUs buy products 1–10 at the same moment, with a correctness check on `GET /admin/oversold` | more `201`s than units in stock, `shoplab_oversold_total`, `GET /admin/oversold` |
| 6 | `SHOPLAB_FIX_LEAK` | **Session leak.** The in-memory session store never evicts expired sessions, and each session pins a filled 16 KiB buffer. The fix adds a TTL janitor and drops the buffer. | `POST /api/login` | Soak test that logs in on every iteration for 30+ minutes | `process_resident_memory_bytes`, `go_memstats_heap_inuse_bytes`, `shoplab_sessions`, `shoplab_session_store_bytes` |

### How the oversell is detected

Each product row keeps a small inventory ledger: `units_received` and
`units_sold`. Checkout always increments `units_sold` atomically, even on the
buggy path, so this holds in a correct system:

```
stock = units_received - units_sold
```

`GET /admin/oversold` lists every product where `units_sold > units_received`
(sold units that never existed) or where `stock` has drifted from the ledger
(lost updates). After each checkout commits, the units it oversold are added to
`shoplab_oversold_total`. The schema leaves out `CHECK (stock >= 0)` on purpose:
the buggy path never writes a negative number, it overwrites the stock with
stale values. `POST /admin/stock/reset` (requires `SHOPLAB_ADMIN=1`) sets
products 1–10 back to 5 units so the demo can be repeated.

### Measured on a laptop

These are single runs on Apple Silicon with Docker Desktop, using the compose
stack seeded with `--orders-per-user 200`. A throwaway Go load generator
produced them: first with every bottleneck active, then with
`SHOPLAB_FIX_ALL=1`. The numbers will differ on your machine, but the
direction should not.

| Scenario | Bottlenecks active | All fixes on |
|---|---|---|
| `GET /api/products?page=1..50`, 50 concurrent, 10 s (N+1 and pool) | 782 req/s, p50 60 ms, p95 88 ms | 3,524 req/s, p50 10 ms, p95 37 ms |
| `GET /api/orders`, 50 users, 10 s (index) | 173 req/s, p50 283 ms, p95 356 ms | 6,208 req/s, p50 6 ms, p95 19 ms |
| Cold cache, `GET /api/products/{1..20}`, 200 concurrent, 25 s (cache) | 660 recomputations, max 5.0 s | 70 recomputations, max 291 ms |
| 60 buyers, 5 units, simultaneous checkout (race) | 25 orders placed, 20 units oversold | 5 orders placed, 55 × 409 |
| `POST /api/login`, 20 concurrent, 15 s, about 45k sessions (leak) | session store 742 MB, RSS 52 → 870 MiB | session store 17 MB, RSS 57 → 80 MiB |

The integration tests check the same effects at a lower level:

```
fix_n1=false: 42 statements per 20-item page
fix_n1=true:  2 statements per 20-item page
max_conns=5:  40 concurrent product details, 152 acquisitions waited, 12.7s total wait
max_conns=40: 40 concurrent product details, 0 acquisitions waited
fix_cache=false: 100 concurrent cold requests -> 100 expensive recomputations in 1.07s
fix_cache=true:  100 concurrent cold requests -> 1 expensive recomputations in 20ms
fix_race=false: 40 checkouts succeeded for 5 units, 35 units reported oversold
fix_race=true:  5 checkouts succeeded, 35 out of stock, 0 reported oversold
```

The index test checks that the query plan switches from `Seq Scan on orders`
to `orders_user_created_idx`. The session unit test shows 2,000 expired leaky
sessions retaining about 32 MiB of heap, against roughly zero once fixed.

## API

The full contract is the embedded OpenAPI 3.1 document at
[`GET /openapi.yaml`](openapi.yaml). It has schemas and examples for every
endpoint, and a test keeps it in sync with the router.

| Method | Path | Auth | Notes |
|---|---|---|---|
| GET | `/` | – | Name, version and active fixes |
| GET | `/healthz` | – | Liveness |
| GET | `/readyz` | – | Pings Postgres and Redis; returns 503 if either is down |
| GET | `/metrics` | – | Prometheus |
| GET | `/openapi.yaml` | – | This API's OpenAPI 3.1 spec |
| GET | `/api/products?page=&per_page=&q=&category=` | – | Category name, stock, average rating; `category` is a slug or an id |
| GET | `/api/products/{id}` | – | Category, stock, review summary (average, count, histogram, highlights, 5 latest); `X-Cache: hit\|miss\|stale` |
| POST | `/api/login` | – | `{email,password}` → `{token,tokenType,expiresAt}`; the token is 32 random bytes, hex-encoded |
| GET | `/api/me` | Bearer | The current user |
| GET | `/api/cart` | Bearer | Cart stored in Redis for each session |
| POST | `/api/cart` | Bearer | `{productId, qty}` sets that product's quantity (1–99) |
| DELETE | `/api/cart/{productId}` | Bearer | Removes a line |
| POST | `/api/checkout` | Bearer | `201 {orderId,total,…}`, `409 out_of_stock`, `400 empty_cart` |
| GET | `/api/orders?page=&per_page=` | Bearer | The user's orders, newest first |
| GET | `/api/orders/{id}` | Bearer | One order with its items; another user's order returns 404 |
| GET | `/admin/oversold` | – | Oversell report (read-only) |
| POST | `/admin/cache/flush` | `SHOPLAB_ADMIN=1` | Drops every cached product detail |
| POST | `/admin/stock/reset` | `SHOPLAB_ADMIN=1` | Sets products 1–10 back to 5 units |

Every error has the same shape:

```json
{"error":{"code":"out_of_stock","message":"product 1 is out of stock (requested 1, available 0)","details":{"productId":1,"requested":1,"available":0}}}
```

Money is returned twice: as integer cents (`priceCents`, `totalCents`), which
are authoritative, and as a dollar number for display (`price`, `total`).
Checkout adds 8.25% tax, plus $4.99 shipping on orders under $50.

### Metrics

| Metric | Type | Notes |
|---|---|---|
| `shoplab_http_request_duration_seconds{route,method,status}` | histogram | `route` is the chi pattern, e.g. `/api/products/{id}` |
| `shoplab_http_requests_in_flight` | gauge | |
| `shoplab_db_queries_total{result}` | counter | Every SQL statement; reveals N+1 |
| `shoplab_db_pool_{acquired,idle,total,max}_conns` | gauge | pgxpool stats |
| `shoplab_db_pool_acquire_total`, `…_acquire_duration_seconds_total` | counter | |
| `shoplab_db_pool_wait_total`, `…_wait_duration_seconds_total` | counter | Acquisitions that had to wait for a free connection |
| `shoplab_db_pool_canceled_acquire_total` | counter | Requests that gave up while waiting |
| `shoplab_cache_requests_total{result}`, `shoplab_cache_loads_total`, `shoplab_cache_load_duration_seconds` | counter / histogram | Stampede signal |
| `shoplab_sessions`, `shoplab_session_store_bytes` | gauge | Leak signal |
| `shoplab_oversold_total` | counter | Units sold beyond stock |
| `shoplab_checkouts_total{result}`, `shoplab_logins_total{result}` | counter | |
| `shoplab_fix_enabled{fix}` | gauge | 1 when a fix is on |
| `go_*`, `process_*` | | Go runtime and process collectors |

## Tests

```sh
go vet ./... && go test ./...        # unit tests; no database needed

docker compose up -d postgres redis
DATABASE_URL='postgres://shoplab:shoplab@127.0.0.1:55432/shoplab?sslmode=disable' \
REDIS_URL='redis://127.0.0.1:56379/0' \
go test -tags integration -v ./integration/
```

The integration tests seed a small dataset if the database is empty. They also
add orders, reset stock on products 1–10, flush the product cache and toggle
the orders index (putting it back afterwards). Do not point them at data you
care about.

## Layout

```
cmd/shoplab/        main: serve, seed, migrate, users-csv, healthcheck
internal/api/       chi router, handlers, middleware, JSON errors
internal/cache/     read-through cache (naive vs singleflight + jitter + SWR)
internal/config/    env parsing
internal/db/        pool, embedded migrations, index toggle
internal/metrics/   Prometheus registry, pool collector, query counter
internal/redisstore Redis carts and cache KV
internal/seed/      deterministic data generator
internal/session/   in-memory session store (leaky vs janitor)
internal/shop/      domain types and interfaces
internal/store/     Postgres queries (N+1 listing, checkout race)
integration/        tests against real Postgres and Redis (build tag: integration)
migrations/         embedded SQL
openapi.yaml        embedded OpenAPI 3.1 spec
data/users.csv      data feeder for 1,000 users
```

ShopLab is its own Go module and imports nothing from the Stampede module.
