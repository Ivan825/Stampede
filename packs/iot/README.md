# IoT platforms pack

Journeys, stresses and targets for IoT platforms whose devices speak MQTT.
The scenarios use the [mqtt plugin](../../plugins/mqtt/README.md):

```sh
stampede plugin install mqtt
```

| File | What it tests |
|---|---|
| `journeys/device-mix.yaml` | Everyday mix: connected devices report a reading and wait for the platform's ack 70%, devices fetch their desired state (shadow) 15%, operators read the fleet and change device settings over HTTP 15% |
| `stresses/connected-fleet.yaml` | 2,000 devices connected at once, each reporting every 10 to 20 seconds |
| `stresses/telemetry-burst.yaml` | Devices go from 50 to 500, each reporting every 100 to 300ms, then back down: ingest has to keep up |
| `stresses/reconnect-storm.yaml` | The whole fleet reconnects at once (spike from 10 to 300 connections a second): connect, subscribe, report, disconnect |
| `targets.yaml` | Default targets: stored p95 under 500ms, report p99 under 200ms, connect p99 under 1s, under 1% errors |

The scenarios follow IoTLab's topics: device `dev-N` signs in with client
id and username `dev-N`, publishes readings (JSON with a `seq`) to
`devices/dev-N/telemetry`, and the platform acknowledges each stored
reading on `devices/dev-N/acks` with the same `seq`. The `stored` step's
latency is from the publish to that ack: the ingest delay. A device
asks for its desired state on `devices/dev-N/shadow/get` and gets it on
`devices/dev-N/shadow`. Each virtual user is one device and stays
connected across iterations, as a device would. Every journey and stress
is run against [IoTLab](../../examples/packlab/README.md#iotlab) in CI.

For your own platform, change the topics, the payloads and the
credentials (`vars.password`; use a data file for per-device secrets). If
your platform does not acknowledge readings, subscribe a second device or
a test consumer to the telemetry topic and `expect` the reading there
instead. Scenarios made only of MQTT steps have no `target.baseURL`, so
`stampede run` reaches private hosts only; add `--allow-host` for a broker
elsewhere.

```sh
stampede plugin install mqtt
go run ./examples/packlab -product iot               # IoTLab: HTTP on :8110, MQTT on :8111
stampede init --target http://localhost:8110 -e MQTT_BROKER=tcp://localhost:8111   # detects this pack
stampede run stampede/iot/journeys/device-mix.yaml \
  -e TARGET_URL=http://localhost:8110 -e MQTT_BROKER=tcp://localhost:8111
```
