# Databases and caches pack

Journeys, stresses and targets run straight against a PostgreSQL database
and a Redis cache, without the application in front. The scenarios use
the [sql](../../plugins/sql/README.md) and
[redis](../../plugins/redis/README.md) plugins:

```sh
stampede plugin install sql
stampede plugin install redis
```

| File | What it tests |
|---|---|
| `journeys/query-mix.yaml` | Everyday mix: a cached product, a product row and a customer's order history 50%; a checkout (insert an order, take stock, count the sale in Redis) 25%; sessions in Redis 15%; a day's revenue by region and the top products 10% |
| `stresses/connection-pool.yaml` | Up to 300 users sharing a pool of 20 connections: the wait for a connection shows in every query's latency |
| `stresses/write-contention.yaml` | Up to 50 concurrent checkouts on five best sellers: writes waiting on each other's row locks |
| `targets.yaml` | Default targets: product p95 under 10ms, order history p95 under 50ms, place order p95 under 50ms, cached product p99 under 5ms, under 1% errors |

The SQL follows DBLab's schema: `customers (id, name, email, region)`,
`products (id, name, price, stock)` and `orders (id, customer_id,
product_id, qty, total, created_at)`, PostgreSQL syntax with `$1`
placeholders. In Redis, each product is a hash `product:<id>` and
`popular` is a sorted set of product ids. Each virtual user holds its own
database and Redis connection unless a step says `pool: shared`. Every
journey and stress is run against
[DBLab](../../examples/packlab/README.md#dblab) in CI, with PostgreSQL 16
as a service container and Redis in-process (miniredis).

For your own database, replace the SQL with the queries your application
runs most (`pg_stat_statements` lists them), keep the arguments spread
over realistic ids, and point `SQL_DSN` at a copy of production-sized
data, not production: these scenarios write. For MySQL, set `driver:
mysql` and use `?` placeholders. A database cannot be detected over
HTTP, so `stampede init` does not offer this pack: install it with
`stampede pack install databases`. The scenarios have no
`target.baseURL`, so `stampede run` reaches private hosts only; add
`--allow-host` for a database elsewhere.

```sh
stampede plugin install sql && stampede plugin install redis
PACKLAB_POSTGRES_DSN=postgres://localhost:5432/lab?sslmode=disable \
  go run ./examples/packlab -product databases       # DBLab: seeds PostgreSQL, Redis on :8115
stampede pack install databases
stampede run stampede/databases/journeys/query-mix.yaml \
  -e SQL_DSN='postgres://localhost:5432/lab?sslmode=disable&search_path=dblab' -e REDIS_ADDR=localhost:8115
```
