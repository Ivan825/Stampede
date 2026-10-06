# SQL plugin

Runs queries and statements straight against a database: PostgreSQL
([pgx](https://github.com/jackc/pgx)), MySQL
([go-sql-driver](https://github.com/go-sql-driver/mysql)) and SQLite
([modernc.org/sqlite](https://gitlab.com/cznic/sqlite), pure Go). For
finding a query mix's limits or connection-pool exhaustion without going
through the application.

```sh
stampede plugin install sql
```

## Steps

```yaml
- name: product page
  plugin: sql.query
  with:
    driver: postgres                 # postgres, mysql or sqlite
    dsn: ${secret.DATABASE_URL}      # the target policy applies to its host
    sql: SELECT id, name, stock FROM products WHERE id = $1
    args: ["${rand(1, 50)}"]         # positional: $1 (PostgreSQL, SQLite) or ? (MySQL, SQLite)
    rows: 10                         # also return up to 10 rows (default: only the first)
  check: { json: { "$.rowCount": 1 } }
  extract: { stock: "$.first.stock" }

- name: reserve stock
  plugin: sql.exec
  with:
    driver: postgres
    dsn: ${secret.DATABASE_URL}
    sql: UPDATE products SET stock = stock - 1 WHERE id = $1 AND stock > 0
    args: ["${productId}"]
    pool: shared                     # per-vu (default) or shared
    maxConns: 20                     # size of the shared pool (default 10)
  check: { json: { "$.rowsAffected": 1 } }
```

DSNs: `postgres://user:pass@host:5432/db?sslmode=disable` or
`host=db user=app ...` for PostgreSQL; `user:pass@tcp(host:3306)/db` for
MySQL; `file:/path/app.db?_pragma=busy_timeout(5000)` or `:memory:` for
SQLite (use a `file:` URI: a bare path is not recognised by the target
policy and is refused when a policy applies).

**Connections.** With `pool: per-vu` (the default) each virtual user
holds one connection per database for the whole run, like a fleet of
application instances with a pool of one. With `pool: shared` every user
of the worker's plugin process shares one pool of `maxConns`
connections, which shows what happens when a service's pool runs dry:
users queue for a connection and that time is part of the latency.

**Latency** is from sending the statement to reading the last row. For
queries, the `wait` phase is until the first result arrived and
`download` is reading the rows. Bytes in count the row data read.

**Returned values**: `query` returns `rowCount`, `columns`, `first` (the
first row as `{column: value}`, or null) and, with `rows: N`, `rows` (up
to N rows). `exec` returns `rowsAffected` and `lastInsertId` (where the
driver supports it). Text columns are strings, numbers are numbers, times
are RFC 3339 strings, other binary data is base64.

**Errors** carry the database's own code where there is one:
`sql <SQLSTATE>` for PostgreSQL and MySQL (`sql 23505` unique violation,
`sql 40001` serialization failure, `sql 53300` too many connections,
`sql 23000` MySQL integrity violation), `sql SQLITE_<CODE>` for SQLite
(`sql SQLITE_BUSY`, `sql SQLITE_CONSTRAINT`), plus `sql timeout`,
`sql connection error`, `sql error` and `invalid config`.

## Example

[examples/catalog.yaml](examples/catalog.yaml) reads products and
reserves stock:

```sh
stampede run plugins/sql/examples/catalog.yaml \
  -e SQL_DRIVER=postgres -e SQL_DSN=postgres://app:app@localhost:5432/shop
```

## Tests

`go test ./...` here runs the steps against SQLite (in-process): DDL,
inserts with parameters, queries with rows, errors with their codes, the
shared pool, the example through Stampede's engine and the
[conformance suite](../../docs/plugins.md#conformance). The same checks
run against PostgreSQL and MySQL when `STAMPEDE_TEST_POSTGRES_DSN` and
`STAMPEDE_TEST_MYSQL_DSN` are set, which the CI `plugins` job does with
service containers; connection failures to both are tested everywhere.
