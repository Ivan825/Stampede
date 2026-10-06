# UDP plugin

Sends UDP datagrams from each virtual user's own socket and, optionally,
waits for a reply and times the round trip. For game servers, DNS-like
request/reply services, telemetry collectors and anything else that
speaks datagrams.

```sh
stampede plugin install udp
```

## `udp.send`

```yaml
- name: ping
  plugin: udp.send
  with:
    addr: game.internal:7777       # host:port; the target policy applies to it
    payload: "ping ${vu} ${iter}"
    encoding: text                 # text (default), hex or base64
    reply: true                    # wait for a reply (default false)
    match: "ping ${vu} ${iter}"    # regex the reply must match; others are skipped
    maxReply: 2048                 # largest reply read, bytes (default 65535)
  check: { maxLatency: 50ms, json: { "$.reply": exists } }
  extract: { pong: "$.reply" }
  timeout: 1s                      # how long to wait for the reply
```

Each virtual user opens one connected socket per address and keeps it
for the whole run, so it keeps its source port like a real client.

**Latency** is the time to write the datagram, or with `reply: true` the
round trip from the write to the matching reply (also reported as the
`wait` phase). Bytes sent and received are counted.

**Returned values**: `bytesSent`; with a reply also `reply` (the reply as
text, or base64 with `replyEncoding: base64` when it is not valid UTF-8),
`replyHex` and `replyBytes`.

**Errors**: `udp timeout` (no matching reply in time), `udp refused` (the
host answered with ICMP port unreachable), `udp error`, `invalid config`.

UDP does not pair replies with requests. A reply that arrives after its
step timed out is read by the next step that waits on the same address;
use `match` with something unique to each request (as above) to skip it.

## Example

[examples/game-ping.yaml](examples/game-ping.yaml) pings an echo server
and sends binary position updates:

```sh
stampede run plugins/udp/examples/game-ping.yaml -e UDP_ADDR=127.0.0.1:7777
```

## Tests

`go test ./...` in this directory runs the step against a local UDP echo
listener, runs the example through Stampede's engine and runs the
[conformance suite](../../docs/plugins.md#conformance).
