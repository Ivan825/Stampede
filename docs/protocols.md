# Protocols

Besides plain HTTP requests (`get: /path`, `post: /path`, ...), a step can
speak GraphQL, server-sent events, WebSocket or gRPC, drive a real
browser page, or speak any protocol a
[plugin](plugins.md) adds (MQTT, Kafka, Redis, SQL and UDP plugins are in
this repository). Every protocol step:

- is checked when the scenario is loaded: unknown keys are rejected with
  their line number, templates and regexes are compiled, and a variable
  must be extracted (or declared under `vars`) before it is used;
- is recorded like an HTTP request, with its own latency histogram, error
  rate and error labels, and can be named in targets
  (`journey/step.p95 < 300ms`);
- passes the same safety host policy as HTTP requests;
- carries W3C trace context (`traceparent`, and `baggage` with `stampede.run_id`, `stampede.vu`, `stampede.journey` and `stampede.step`)
  as headers or gRPC metadata;
- can `extract` variables that later steps of any kind use.

A failed step ends its iteration, as for HTTP.

| Step | Latency is | Error labels |
|---|---|---|
| `graphql` | the HTTP exchange (both round trips for a persisted-query miss) | `graphql error`, `graphql invalid response`, `graphql persisted query not found`, plus HTTP labels |
| `sse` | the whole stream; time to first event is reported separately | `sse no events`, `sse no match`, `sse ended early`, `sse not an event stream`, `sse event too large`, plus HTTP labels |
| `ws` | the opening handshake | `ws handshake failed`, `HTTP 403`, network labels |
| `send` (in `ws`) | writing the message | `ws closed`, `ws error` |
| `expect` (in `ws`) | from the last `send` to the matching message | `ws expect timeout`, `ws closed`, `ws message too large` |
| `grpc` | the call (all messages of a server stream) | `gRPC <STATUS>`, `check status (gRPC <STATUS>)`, `grpc unknown method`, `grpc reflection failed`, `grpc invalid message` |
| `browser`, `goto` | the page load, from navigation to the load event | `HTTP 404` and other statuses, `browser net::ERR_...`, `timeout`, `browser unavailable`, `blocked by safety` |
| `click`, `fill`, `press`, `waitFor` | the action, including waiting for the element | `timeout`, `browser error` |
| `assert` (in `browser`) | reading the elements' text | `check failed`, `timeout` |
| `plugin` | measured by the plugin around its operation | the plugin's own (such as `mqtt timeout`, `sql 23505`), `invalid config`, `plugin crashed`, `plugin unavailable`, `timeout` |

## GraphQL

```yaml
- name: product
  graphql: /graphql                 # endpoint; POSTed as JSON
  query: |
    query Product($id: ID!) { product(id: $id) { id name price } }
  variables: { id: "${productId}" } # strings may contain ${} expressions
  operationName: Product
  persisted: true                   # automatic persisted query (optional)
  headers: { Authorization: "Bearer ${token}" }
  check:
    json: { "$.data.product.id": exists }
    allowErrors: false              # default
  extract: { name: "$.data.product.name" }
  timeout: 5s
```

The operation goes through the HTTP driver, so cookies, headers,
connection reuse and `target.http` options apply. A response whose
`errors` array is not empty fails the step as `graphql error`, even with
HTTP 200, unless `check.allowErrors: true`. Checks and extractors work on
the response JSON as for HTTP requests.

`persisted: true` sends an [automatic persisted
query](https://www.apollographql.com/docs/apollo-server/performance/apq):
first only the query's SHA-256, and the full query only if the server
answers `PersistedQueryNotFound`. `persisted: {sha256: <hex>}` sends a hash
the server already knows; `query` can then be left out.

## Server-sent events

```yaml
- name: completion
  sse: /v1/chat/completions
  json:                              # a body makes the method POST
    model: small
    stream: true
    messages: [{ role: user, content: "Summarise order ${orderId}" }]
  until:
    match: '"finish_reason":"stop"'  # regex over each event's data
    duration: 60s                    # cap
  check: { status: 200 }
  extract: { reply: "$.choices[0].delta.content" }
```

`sse:` takes the same request keys as an HTTP step (`method`, `headers`,
`query`, `json`, `body`, `form`, `check`, `extract`, `timeout`). The method
is GET, or POST when a body is set. The response must be
`text/event-stream`.

`until` says when to stop reading:

- `events: N` — stop after N events; fewer fails the step (`sse ended early`);
- `match: regex` — stop at the first event whose data matches; none fails
  the step (`sse no match`);
- `duration: 30s` — stop after this long. On its own this ends the step
  successfully; with `events` or `match`, reaching it first is a failure.

With both `events` and `match`, the stream stops at whichever comes first
and both must be met. With no `until`, the step reads until the server
closes the stream. A stream with no events fails (`sse no events`).

Checks and extractors see the data of the matching event (or the last event
read) as the response body. To extract from the final chunk of an OpenAI-style
stream, stop on that chunk (`"finish_reason":"stop"`) rather than on the
`[DONE]` sentinel, which is not JSON.

**Time to first token, tokens per second.** The step's latency is the
whole stream. Stampede also records the time from the start of the step to
the first event, and the rate of the events after it. Reports show them per
streaming step:

```
  stream                                         events first p50 first p95 first p99   events/s
  chat › completion                                4000   153.1ms   156.8ms   157.3ms       45.8
```

In the JSON report they are under `journeys[].steps[].stream`
(`firstEvent.{mean,p50,p95,p99}` in seconds, `events`, `eventsPerSec`).

## WebSocket

```yaml
- name: connect
  ws: /ws/chat                       # ws://, wss://, http(s):// or a path
  headers: { Authorization: "Bearer ${token}" }
  subprotocols: [chat.v1]
  timeout: 5s                        # handshake
  steps:
    - name: welcome
      expect: { json: { "$.type": welcome } }
      extract: { room: "$.room" }
    - send: { type: join, room: "${room}" }   # JSON, sent as text
    - loop: 10
      steps:
        - send: 'say ${iter}'                 # text
        - name: echo
          expect: { match: '^said', timeout: 2s }
        - think: 1s..3s
- get: /api/rooms/${room}             # variables outlive the block
```

A `ws` step opens a connection, runs its `steps` with it, and closes it
(with a normal close handshake, in the background) when they finish or the
iteration fails. Inside the block, `send` writes a text message (a string
template, or a JSON value whose strings may contain `${}`) and `expect`
waits for a message. Any other step kind can be mixed in; `send` and
`expect` outside a `ws` block, and nested `ws` blocks, are rejected when
the scenario loads.

`expect` takes a regex (`expect: '^pong'`) or `{match, json, timeout}`. A
message must satisfy every condition given; other messages are skipped. With
no condition the next message matches. `timeout` defaults to
`target.timeout`. `extract` reads the matching message with `$.path`,
`regex:` or `body`. The `match` and `json` values are literal, not
templates.

The handshake goes through an HTTP/1.1 transport (a WebSocket cannot ride
HTTP/2) with the user's cookies, and records DNS, connect, TLS and wait
phases. An `expect`'s latency runs from the last `send` on the connection
(or the handshake, before any send) to the arrival of the matching message,
so time the message waited in the queue while other steps ran is not
counted.

Each connection has one reader goroutine and a queue of 32 messages for
later `expect` steps; when the queue is full the oldest message is dropped,
so pings and the close handshake are always answered. One worker has held
9,500 connections for 50 seconds (475,000 messages, 0 errors) in 458 MB of
memory; past about 10,000 connections the operating system's
file-descriptor and ephemeral-port limits apply, so raise `ulimit -n` and
spread load over workers or source addresses.

## gRPC

```yaml
- name: get product
  grpc: shop.v1.Catalog/GetProduct   # package.Service/Method
  target: grpc://catalog:9090        # optional; see below
  message: { id: "${productId}", fields: [name, price] }
  metadata: { authorization: "Bearer ${token}" }
  check:
    status: [OK, NOT_FOUND]          # default OK
    json: { "$.name": exists }
    maxLatency: 200ms
  extract: { name: "$.name", region: "header:x-region" }
  timeout: 2s
```

Unary and server-streaming methods are supported; client and bidirectional
streaming are rejected before the run.

**Target.** `grpc://host:port` is plaintext HTTP/2; `grpcs://host:port` is
TLS (`target.http.insecureSkipVerify` applies). Without `target`, calls go
to the host of `target.baseURL` (TLS when it is `https://`). `target` may
use `env`, `secret` and `vars`; it is rendered once when the run starts.
Calls share four HTTP/2 connections per target.

**Descriptors.** By default the method is looked up with the server's
reflection service (v1, falling back to v1alpha) on first use, and cached
for the run. Without reflection, give the descriptors as files, which are
loaded before the run starts:

```yaml
  protoset: protos/shop.protoset     # protoc --descriptor_set_out --include_imports, or buf build -o
  # or
  proto: [shop/v1/catalog.proto]     # compiled by Stampede; well-known types are built in
  importPaths: [protos]              # default: the scenario file's directory
```

Paths are relative to the scenario file.

**Messages.** `message` is the request as JSON with protojson field names
(`productId` or `product_id`); strings may contain `${}`. The response is
rendered as protojson, so `check.json` and `$.path` extractors work as for
HTTP; a server stream's messages form a JSON array (`$.length`,
`$[0].price`). `header:name` reads response headers and trailers.
`check.status` takes code names in any spelling (`NOT_FOUND`, `NotFound`).
In `check.expr`, `status` is the numeric code.

A server stream records its messages, time to first message and message
rate, reported like an SSE stream.

## Browser

A `browser` step opens its URL in a real page of headless Chrome, as one
visitor would, and runs browser actions in it. Every iteration gets a fresh
browser context, so cookies and storage start empty, and every request the
page makes (scripts, images, API calls) passes the same host policy as
other steps: a page cannot pull load onto a third-party CDN unless that host
is allowed with `--allow-host` (or the target's `allowHosts`).

```yaml
journeys:
  - name: checkout
    steps:
      - name: home
        browser: /                       # opens the page: one measured page load
        viewport: { width: 390, height: 844 }
        timeout: 20s                     # per action (default the target timeout)
        steps:
          - click: "a.product"           # waits until the element is visible
          - waitFor: "#add-to-cart"
          - click: "#add-to-cart"
          - goto: /checkout              # another measured page load
          - fill: { "#email": "${data.users.email}", "#card": "4242 4242 4242 4242" }
          - press: Enter                 # Enter, Tab, Escape, ArrowDown ... or a character
          - assert: { ".order-status": "Thank you" }
targets:
  - checkout/home.p95 < 3s
```

Selectors are CSS selectors and every value may use `${}` expressions.
`fill` types the text into each field after clearing it; `assert` fails the
step as a check when an element's text does not contain the value.

**Web Vitals.** Page loads (`browser` and `goto`) record time to first
byte, first contentful paint, largest contentful paint, cumulative layout
shift and the load event; clicks and key presses record interaction to
next paint. Reports show them per step as mean and p95 under *Web vitals*.

**Chrome.** The worker or CLI running the scenario needs Chrome or
Chromium: it is found on `PATH` (`chromium`, `google-chrome`, ...), in the
standard install location, or at `STAMPEDE_CHROME`. A run with browser
steps fails before any load when it is missing, and a worker without it
refuses such runs. Workers report `browser` among their protocols when
they have it. The `ghcr.io/ivan825/stampede-browser-worker` image (built
from [`deploy/docker/Dockerfile.browser-worker`](../deploy/docker/Dockerfile.browser-worker))
bundles Chromium; inside a container Chrome's own sandbox is turned off
with `STAMPEDE_CHROME_NO_SANDBOX=true`, because the container is the
sandbox.

**Cost.** Each browser user is a real Chrome page: count on about one
virtual user per 100–200 MB of memory and a fraction of a CPU core, against
thousands of HTTP users per core. Use browser steps for the journeys whose
front-end experience you need to measure, and HTTP steps for the bulk of
the load. A published per-worker capacity figure is planned.

## Plugin steps

```yaml
- name: publish reading
  plugin: mqtt.publish              # <plugin>.<step>
  with:                             # the step's settings; strings may contain ${}
    topic: devices/${vu}/telemetry
    payload: '{"seq": ${iter}}'
    qos: 1
  check: { maxLatency: 50ms, json: { "$.messageId": exists } }
  extract: { msgId: "$.messageId" }
  timeout: 5s
```

A plugin step runs in a plugin: a separate executable installed with
`stampede plugin install <name>`. `with` is checked against the schema the
plugin describes, checks and extractors work on the JSON object the step
returns, and the engine applies the target policy to the address the
plugin marks. Each user has its own session (connection, client, socket)
in each plugin, kept across iterations. See [plugins](plugins.md) for the
details, the first-party plugins and writing your own.

## HTTP/2

```yaml
target:
  baseURL: https://shop.test
  http:
    http2: true        # negotiate HTTP/2 over TLS when the server offers it
    # h2c: true        # HTTP/2 without TLS to http:// targets (prior knowledge)
```

Each request records the protocol that was actually negotiated; the JSON
report counts them per step under `protocols` (`"HTTP/2.0": 1200`), so you
can confirm `http2: true` took effect.
