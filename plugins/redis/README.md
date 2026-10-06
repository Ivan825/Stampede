# Redis plugin

Runs any Redis command, or a pipeline of commands, from each virtual
user's own connection (built on [go-redis](https://github.com/redis/go-redis)).

```sh
stampede plugin install redis
```

## `redis.command`

```yaml
- name: read session
  plugin: redis.command
  with:
    addr: cache.internal:6379        # or redis://user:pass@host:6379/2, rediss:// for TLS
    command: [GET, "session:${vu}"]  # a list, or a string split on spaces: "GET session:1"
  check: { json: { "$.value": exists } }
  extract: { session: "$.value" }

- name: touch and count
  plugin: redis.command
  with:
    addr: cache.internal:6379
    pipeline:                        # sent together in one round trip
      - [EXPIRE, "session:${vu}", 1800]
      - [INCR, stats:views]
  extract: { views: "$.values[1]" }
```

| Setting | |
|---|---|
| `addr` | `host:port` or a `redis://` / `rediss://` URL. The target policy applies to it. |
| `command` | One command: a list (`[SET, k, v]`, numbers allowed) or a string split on spaces. |
| `pipeline` | A list of commands sent in one round trip. Give `command` or `pipeline`. |
| `username`, `password`, `db` | Override what the URL says. |
| `tls`, `insecureSkipVerify` | TLS for `host:port` addresses; skip certificate checks for test servers. |

Each virtual user holds one connection per server (per address and
credentials) for the whole run, like a client process with a pool of one.
Commands are not retried.

**Latency** is the round trip of the command or of the whole pipeline.
Bytes out are the commands' size in the Redis protocol; bytes in are the
replies' payload bytes (an approximation: framing is not counted).

**Returned values**: `value` for a command, `values` (a list, in order)
for a pipeline. A missing key is `null`. Bulk strings are strings,
integers are numbers, arrays are lists. In a pipeline, a command that
failed appears as `{"error": "..."}` and fails the step.

**Errors**: `redis <CODE>` for error replies, by the reply's code
(`redis ERR`, `redis WRONGTYPE`, `redis NOAUTH`, `redis MOVED`...),
`redis timeout`, `redis connection error`, `invalid config`.

## Example

[examples/session-cache.yaml](examples/session-cache.yaml) stores, reads
and refreshes sessions and counts views:

```sh
stampede run plugins/redis/examples/session-cache.yaml -e REDIS_ADDR=localhost:6379
```

## Tests

`go test ./...` here runs against [miniredis](https://github.com/alicebob/miniredis)
(an in-process Redis): commands, pipelines, error codes, authentication,
a server going away, the example through Stampede's engine, and the
[conformance suite](../../docs/plugins.md#conformance). It has not been run
against a real Redis server in this repository's CI.
