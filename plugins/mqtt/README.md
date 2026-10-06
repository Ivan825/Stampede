# MQTT plugin

Each virtual user is an MQTT 3.1.1 client (built on
[Eclipse Paho](https://github.com/eclipse/paho.mqtt.golang)) that connects
to a broker, publishes, subscribes and waits for messages: for IoT
platforms, telemetry ingest and MQTT-based messaging.

```sh
stampede plugin install mqtt
```

## Steps

```yaml
- name: connect
  plugin: mqtt.connect
  with:
    broker: tcp://broker.internal:1883   # tcp/mqtt, ssl/mqtts, ws, wss; the target policy applies
    clientId: "device-${vu}"             # default stampede-<run>-<vu>
    username: device
    password: ${secret.MQTT_PASSWORD}
    cleanSession: true                   # default true
    keepAlive: 30s                       # default 30s
    # insecureSkipVerify: true           # test brokers with self-signed certificates
    # fresh: true                        # reconnect even if connected
- plugin: mqtt.subscribe
  with: { topic: "devices/${vu}/#", qos: 1 }
- name: publish telemetry
  plugin: mqtt.publish
  with: { topic: "devices/${vu}/telemetry", payload: '{"seq": ${iter}}', qos: 1, retain: false }
- name: delivered
  plugin: mqtt.expect
  with:
    topic: "devices/${vu}/telemetry"     # a topic filter (+ and # work); default any
    match: '"seq"'                       # regex over the payload
    json: { "$.seq": "${iter}" }         # JSONPath -> value, or "exists"
    count: 1                             # how many matching messages (default 1)
  timeout: 5s
- plugin: mqtt.disconnect
```

| Step | Latency | Returned values |
|---|---|---|
| `connect` | until CONNACK (also the `connect` phase) | `clientId`, `sessionPresent`, `reused` |
| `publish` | QoS 0: handing the message to the connection; QoS 1: until PUBACK; QoS 2: until PUBCOMP | `topic`, `qos`, `messageId` (QoS 1 and 2) |
| `subscribe` | until SUBACK | `topic` |
| `expect` | from the user's last `publish` or `subscribe` to the arrival of the last matching message | `topic`, `payload`, `json` (when the payload is JSON), `messages` |
| `disconnect` | sending DISCONNECT | |

**Connections persist.** A user's client stays connected across
iterations, like a device. `connect` on a client that is already
connected to the same broker with the same client id is skipped (nothing
is recorded), and so is a `subscribe` to a filter the client already has,
so a journey can start with both and loop. Use `fresh: true` to reconnect
every time, or `disconnect` to end the session.

**Received messages** on any subscription are kept in order, up to 1,000
per user (the oldest are dropped beyond that), until an `expect` takes
them. `expect` takes the first messages that match all of `topic`,
`match` and `json` and leaves the others for later steps. With `count`
above 1 the step also reports its messages as events with the time to the
first, like a stream.

**Errors**: `mqtt not connected`, `mqtt not authorized`, `mqtt client id
rejected`, `mqtt connection refused`, `mqtt connect failed`, `mqtt
timeout`, `mqtt subscribe rejected`, `mqtt expect timeout`, `mqtt error`,
`invalid config`.

Not supported yet: MQTT 5, client certificates, last will messages.

## Example

[examples/telemetry.yaml](examples/telemetry.yaml): devices stay
connected, publish readings at QoS 1 and measure the broker's delivery
back to themselves:

```sh
stampede run plugins/mqtt/examples/telemetry.yaml -e MQTT_BROKER=tcp://localhost:1883
```

## Tests

`go test ./...` here runs against an in-process
[Mochi MQTT](https://github.com/mochi-mqtt/server) broker: connect,
publish at QoS 0, 1 and 2, subscribe, expect with filters and JSON,
request/reply, authentication failures, the example through Stampede's
engine, and the [conformance suite](../../docs/plugins.md#conformance).
It has not been run against another broker in this repository's CI.
