# Kafka plugin

Virtual users produce Kafka records and consume them, as consumer group
members or straight from partitions (built on
[franz-go](https://github.com/twmb/franz-go)).

```sh
stampede plugin install kafka
```

## `kafka.produce`

```yaml
- name: place order
  plugin: kafka.produce
  with:
    brokers: ["kafka-1:9092", "kafka-2:9092"]   # seed brokers; the target policy applies
    topic: orders
    key: "order-${vu}-${iter}"
    value: '{"user": ${vu}, "total": 42}'
    headers: { source: stampede }
    partition: 3            # optional: this partition (default: by key hash)
    acks: all               # all (default), leader or none
  extract: { partition: "$.partition", offset: "$.offset" }
```

Latency is the produce round trip: until the acknowledgement `acks` asks
for (`none`: until the request is written). Records are sent one at a
time, with no lingering. A `traceparent` header is added with the step's
trace context unless you set one.

Returned values: `topic`, `partition`, `offset`.

## `kafka.consume`

```yaml
- name: order visible
  plugin: kafka.consume
  with:
    brokers: ["kafka-1:9092"]
    topic: orders
    # Either as a consumer group member...
    group: order-checkers
    from: start             # where a new consumer starts: start or end (default end)
    # ...or straight from a partition, optionally at an offset:
    # partition: ${partition}
    # offset: ${offset}
    count: 1                # matching records to wait for (default 1)
    key: "order-${vu}-${iter}"  # only records with this key
    match: '"total"'        # only records whose value matches this regex
    latency: age            # wait (default) or age
  timeout: 10s
```

Each user keeps its consumers for the whole run, so a group member keeps
its assignment and a partition reader keeps its position; `offset`
seeks. Records that do not match are skipped; matching records beyond
`count` are kept for the user's next consume step.

**Latency** is, with `latency: wait`, how long the step waited for its
records; with `latency: age`, how old the last record was when it arrived:
from the timestamp the producer gave it to its receipt, which is
end-to-end delay through the cluster (when producer and consumer clocks
agree, as for read-your-own-write checks). With `count` above 1 the
records are reported as events with the time to the first.

Returned values: `count`, `topic`, `partition`, `offset`, `key`, `value`
(when it is text), `json` (when the value is JSON), `headers`,
`timestamp` (ms), `maxAgeMs` (the oldest matched record's age: a
time-based view of consumer lag).

A group with more members than the topic has partitions leaves some
members without partitions: their consume steps wait until they time
out. Offset-based lag (how far a group is behind the log end) is not
reported yet.

**Errors** for both steps: `kafka <ERROR_CODE>` for broker errors
(`kafka UNKNOWN_TOPIC_OR_PARTITION`, `kafka NOT_LEADER_FOR_PARTITION`...),
`kafka timeout`, `kafka consume timeout`, `kafka connection error`,
`kafka error`, `invalid config`.

TLS (`tls: true`, `insecureSkipVerify`) and SASL (`sasl: {mechanism:
plain | scram-sha-256 | scram-sha-512, username, password}`) are
supported; they are not covered by the tests here.

## Example

[examples/orders.yaml](examples/orders.yaml) places orders and reads each
one back from its partition and offset, timing the record's age:

```sh
stampede run plugins/kafka/examples/orders.yaml -e KAFKA_BROKERS=localhost:9092
```

## Tests

`go test ./...` here runs against franz-go's in-process
[kfake](https://pkg.go.dev/github.com/twmb/franz-go/pkg/kfake) cluster:
producing with each acks setting, reading by partition and offset,
seeking, key and regex filters, a consumer group, timeouts, the example
through Stampede's engine and the
[conformance suite](../../docs/plugins.md#conformance). It has not been
run against a real Kafka cluster in this repository's CI.
