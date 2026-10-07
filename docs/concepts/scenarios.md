# Scenarios and journeys

A **scenario** is one YAML (or JSON) file: what to hit, how users behave, how
much load, and what counts as passing. It is validated against
[a JSON Schema](../../schema/scenario.schema.json), so editors with YAML
language support autocomplete it.

```yaml
apiVersion: stampede.dev/v1
kind: Scenario
metadata: { name: checkout }
target:  { baseURL: "${env.TARGET_URL}" }
journeys: [ ... ]   # what users do
load:     { ... }   # how many, how fast, for how long
targets:  [ ... ]   # pass/fail rules
```

## Journeys

A **journey** is one path a user takes. A scenario usually mixes several,
chosen at random by `weight` for each iteration:

```yaml
journeys:
  - name: browse
    weight: 9
    steps: [ ... ]
  - name: checkout
    weight: 1
    target: { p95: 800ms, errors: 0.5% }   # stricter than the global targets
    steps: [ ... ]
```

Each **iteration** is a fresh session: variables, cookies and data rows start
empty. A step that fails (an HTTP error status, a failed check, a transport
error or a failed extraction) ends that iteration, as a real user would not
carry on, and counts as an error.

## Steps

Each step does exactly one thing.

| Step | Example |
|---|---|
| HTTP request | `get: /api/products?page=${rand(1, 20)}` (also post, put, patch, delete, head, options) |
| Pause | `think: 2s..6s` |
| Weighted choice | `branch: [{weight: 70, steps: [...]}, {weight: 30, steps: [...]}]` |
| Repeat | `loop: 3` with `steps:`; `while: "${more}"` with `max:` and `steps:` |
| Named group | `group: login` with `steps:` |
| Condition on any step | `if: "${token != ''}"` |
| Other protocols | `graphql:`, `ws:`, `sse:`, `grpc:` (see [protocols](../protocols.md)) |

Request steps take `headers`, `query`, one of `json`, `form` or `body`,
`timeout`, `check` and `extract`:

```yaml
- post: /api/login
  json: { email: "${data.users.email}", password: "${secret.SHOP_PASSWORD}" }
  check: { status: 200, json: { "$.token": exists } }
  extract: { token: "$.token" }
```

## Expressions

Anything inside `${...}` is a [CEL](https://cel.dev) expression. Variables
available: everything extracted earlier in the iteration, `vars` (static
values), `env` (`-e KEY=VALUE` or the run's environment), `secret` (stored
secrets), `data` (feeder rows), `vu` and `iter`. Helpers: `rand(a, b)`,
`randFloat()`, `randString(n)`, `randEmail()`, `uuid()`, `pick(list)`,
`now()`, `nowMs()`, `base64(s)`, `urlencode(s)`, `sha256(s)`, `toJSON(v)`.

Stampede checks variable flow before running: using `${token}` before the
step that extracts it is a validation error, not a runtime surprise.

## Checks and extractors

| Check | Meaning |
|---|---|
| `status: 200`, `[200, 201]`, `"2xx"` | allowed statuses |
| `bodyContains: "text"` | substring (templated) |
| `json: { "$.items[0].id": 7, "$.token": exists }` | values at JSONPaths |
| `maxLatency: 500ms` | per-request limit |
| `expr: "status == 200 && json.items.size() > 0"` | any condition over `status`, `headers`, `body`, `json`, `latencyMs` |
| `schema: { type: object, required: [id] }` or `schema: schemas/item.json` | the JSON body matches a JSON Schema (draft 2020-12 unless it says otherwise), inline or from a `.json`/`.yaml` file next to the scenario; the dry run names the first mismatch, such as `/id: got string, want integer` |

On a server, schema files follow the same rule as CSV files: they must be
inside the server's data directory. Inline schemas work everywhere.

| Extractor | Reads |
|---|---|
| `"$.path"` | JSONPath into the body |
| `"header:Location"` | a response header |
| `"cookie:session"` | a cookie the response set |
| `"regex:id=(\\d+)"` | first capture group |
| `"css:h1.title"`, `"css:a.next@href"` | text or attribute of the first match |
| `status`, `body` | the status code or whole body |

## Test data

```yaml
data:
  users:   { csv: data/users.csv, mode: unique }      # each row used once
  queries: { list: [shoe, sock], mode: random }
  ids:     { range: [1, 5000], mode: sequential }
```

Modes: `sequential` (round robin), `random`, `unique` (never reused; split
across workers so two workers never share a row; `onExhausted: stop|wrap`)
and `per-vu` (each user keeps one row). On a server, file feeders must live
in the server's data directory; see [configuration](../reference/configuration.md).

### Generated data

A `generate` feeder makes a new row of fake data every time a journey uses
it, so it never runs out:

```yaml
data:
  people:
    generate:
      name: name            # "Priya Shah"
      email: email          # priya.shah.17@example.test, never repeated in a run
      password: password
      plan: pick(free|pro|team)
      seats: int(1,50)
      avatar: text(200KB)   # a payload of that size
```

| Kind | Value |
|---|---|
| `name`, `firstName`, `lastName` | a person's name; `email` and `username` in the same row match it |
| `email`, `username`, `seq` | unique across the run, all workers included; emails use the reserved `example.test` domain |
| `password` | 16 random letters and digits |
| `phone` | a fictional `555-01xx` number |
| `uuid` | a random UUID |
| `company`, `street`, `city`, `country`, `zip` | addresses and organisations |
| `word`, `sentence`, `paragraph` | filler text |
| `bool`, `date`, `datetime`, `ipv4` | dates within the last five years; IPs from documentation ranges |
| `int(min,max)`, `float(min,max)` | a number in the range (floats to two decimals) |
| `pick(a\|b\|c)` | one of the values |
| `text(size)` | a string of that many bytes (`512`, `64KB`, `5MB`; at most 64 MB), made once per field |

`mode` and `onExhausted` do not apply to generated rows.

### Rows from a database

```yaml
data:
  customers:
    sql:
      driver: postgres                     # or mysql
      dsn: ${secret.STAGING_DB}            # postgres://… or user:pass@tcp(host:3306)/db
      query: SELECT email, password FROM test_accounts WHERE active
    mode: unique
```

The query runs once when the run starts (on each worker, which therefore
needs to reach the database) and its rows work like a CSV file's: modes,
splitting across workers and `onExhausted` all apply. The database host
must pass the same host policy as the run's requests. Keep the DSN in a
secret.

## Targets

```yaml
targets:
  - http.p95 < 500ms          # all requests
  - errors < 1%
  - checkout.p99 < 1s         # one journey
  - "browse/GET /api/products.p95 < 300ms"   # one step
  - rps >= 100/s
  - checks > 99%
```

Metrics: `p50 p90 p95 p99 p99.9 max mean errors checks dropped rps count`.
A target with no data fails rather than passing silently.

The full field list is in the [scenario reference](../reference/scenario.md).
