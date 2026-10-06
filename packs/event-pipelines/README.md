# Event pipelines pack

Journeys, stresses and targets for event pipelines on Kafka: producers,
a consumer group that processes events, and the topic it writes to. The
scenarios use the [kafka plugin](../../plugins/kafka/README.md):

```sh
stampede plugin install kafka
```

| File | What it tests |
|---|---|
| `journeys/order-events.yaml` | Everyday flow: publish an order and wait for it to come out processed 80%, read consumer lag and topics over HTTP 20% |
| `stresses/ingest-ramp.yaml` | Producers ramp from 50 to 500 events a second; one in ten waits for its event at the other end |
| `stresses/backlog-recovery.yaml` | Normal traffic (100 a second), three times as much for three minutes, then normal for five: the backlog builds and has to drain |
| `targets.yaml` | Default targets: publish p99 under 100ms, enriched p95 under 1s, under 1% errors |

The scenarios follow PipelineLab's pipeline: events go to `orders`
(acks=all), the `enricher` consumer group adds the customer to each and
writes it to `orders.enriched` with the same key, partition and
timestamp. The `enriched` step reads that partition, skips everything
but its own key, and with `latency: age` reports how old the event was
when it arrived: the end-to-end delay, which is what consumer lag costs.
Each virtual user keeps its consumers for the whole run. Every journey
and stress is run against
[PipelineLab](../../examples/packlab/README.md#pipelinelab) in CI.

For your own pipeline, change the topics, keys and payloads. If your
consumer writes to another partition than the one it read from, consume
the output topic as a consumer group (`group:` instead of `partition:`)
or point the step at a topic your pipeline keys the same way. Ages are
only right when producer and consumer clocks agree, as they do when one
machine runs both. Scenarios made only of Kafka steps have no
`target.baseURL`, so `stampede run` reaches private hosts only; add
`--allow-host` for brokers elsewhere (a Kafka client also talks to every
broker the cluster advertises, which the target policy cannot see).

```sh
stampede plugin install kafka
go run ./examples/packlab -product event-pipelines   # PipelineLab: HTTP on :8112, Kafka on :8113
stampede init --target http://localhost:8112 -e KAFKA_BROKERS=127.0.0.1:8113   # detects this pack
stampede run stampede/event-pipelines/journeys/order-events.yaml \
  -e TARGET_URL=http://localhost:8112 -e KAFKA_BROKERS=127.0.0.1:8113
```
