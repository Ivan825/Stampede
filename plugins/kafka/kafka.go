package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	"github.com/Ivan825/Stampede/pkg/pluginsdk"
)

const version = "0.1.0"

// connection settings shared by both steps.
const connProps = `
    "brokers": {"type": "array", "minItems": 1, "items": {"type": "string"}, "x-stampede-target": true, "description": "Seed brokers, host:port."},
    "tls": {"type": "boolean"},
    "insecureSkipVerify": {"type": "boolean", "description": "Skip TLS certificate checks (test clusters only)."},
    "sasl": {
      "type": "object", "additionalProperties": false, "required": ["mechanism", "username", "password"],
      "properties": {
        "mechanism": {"enum": ["plain", "scram-sha-256", "scram-sha-512"]},
        "username": {"type": "string"}, "password": {"type": "string"}
      }
    },`

const produceSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["brokers", "topic"],
  "properties": {` + connProps + `
    "topic": {"type": "string", "minLength": 1},
    "key": {"type": "string"},
    "value": {"type": "string"},
    "headers": {"type": "object", "additionalProperties": {"type": "string"}},
    "partition": {"type": "integer", "minimum": 0, "description": "Write to this partition (default: by key hash, or round robin without a key)."},
    "acks": {"enum": ["all", "leader", "none"], "description": "Acknowledgement to wait for (default all)."}
  }
}`

const consumeSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["brokers", "topic"],
  "not": {"required": ["group", "partition"]},
  "properties": {` + connProps + `
    "topic": {"type": "string", "minLength": 1},
    "group": {"type": "string", "description": "Consume as a member of this consumer group."},
    "partition": {"type": "integer", "minimum": 0, "description": "Consume this partition directly (no group)."},
    "offset": {"type": "integer", "minimum": 0, "description": "With partition: read from this offset (seeking if needed)."},
    "from": {"enum": ["start", "end"], "description": "Where a new consumer starts without a committed or given offset (default end)."},
    "count": {"type": "integer", "minimum": 1, "description": "Matching records to wait for (default 1)."},
    "key": {"type": "string", "description": "Only records with this key."},
    "match": {"type": "string", "description": "Only records whose value matches this regex."},
    "latency": {"enum": ["wait", "age"], "description": "wait (default): how long the step waited; age: how old the last record was when it arrived (produce timestamp to receipt)."}
  }
}`

func newPlugin() *pluginsdk.Plugin {
	return &pluginsdk.Plugin{
		Name:        "kafka",
		Version:     version,
		Description: "Produces and consumes Kafka records (franz-go).",
		NewSession: func(context.Context, pluginsdk.SessionInfo) (any, error) {
			return &session{producers: map[string]*kgo.Client{}, consumers: map[string]*consumer{}}, nil
		},
		Steps: []pluginsdk.Step{
			{Name: "produce", Description: "Produce one record; latency waits for the acknowledgement.", Schema: produceSchema, Run: produce},
			{Name: "consume", Description: "Wait for matching records as a group member or from a partition.", Schema: consumeSchema, Run: consume},
		},
	}
}

type session struct {
	producers map[string]*kgo.Client
	consumers map[string]*consumer
}

type consumer struct {
	client  *kgo.Client
	pending []*kgo.Record
	// offsets are the next offsets of directly consumed partitions.
	offsets map[int32]int64
}

func (s *session) Close() error { //nolint:unparam // implements io.Closer
	for _, c := range s.producers {
		c.Close()
	}
	for _, c := range s.consumers {
		c.client.Close()
	}
	return nil
}

type connConfig struct {
	Brokers            []string `json:"brokers"`
	TLS                bool     `json:"tls"`
	InsecureSkipVerify bool     `json:"insecureSkipVerify"`
	SASL               *struct {
		Mechanism string `json:"mechanism"`
		Username  string `json:"username"`
		Password  string `json:"password"`
	} `json:"sasl"`
}

func (c *connConfig) key() string {
	k := strings.Join(c.Brokers, ",") + fmt.Sprintf("|%t|%t", c.TLS, c.InsecureSkipVerify)
	if c.SASL != nil {
		k += "|" + c.SASL.Mechanism + "|" + c.SASL.Username + "|" + c.SASL.Password
	}
	return k
}

func (c *connConfig) opts() []kgo.Opt {
	opts := []kgo.Opt{kgo.SeedBrokers(c.Brokers...), kgo.MetadataMinAge(time.Second)}
	if c.TLS {
		opts = append(opts, kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.InsecureSkipVerify})) //nolint:gosec // requested for test clusters
	}
	if s := c.SASL; s != nil {
		var m sasl.Mechanism
		switch s.Mechanism {
		case "plain":
			m = plain.Auth{User: s.Username, Pass: s.Password}.AsMechanism()
		case "scram-sha-256":
			m = scram.Auth{User: s.Username, Pass: s.Password}.AsSha256Mechanism()
		default:
			m = scram.Auth{User: s.Username, Pass: s.Password}.AsSha512Mechanism()
		}
		opts = append(opts, kgo.SASL(m))
	}
	return opts
}

type produceConfig struct {
	connConfig
	Topic     string            `json:"topic"`
	Key       *string           `json:"key"`
	Value     string            `json:"value"`
	Headers   map[string]string `json:"headers"`
	Partition *int32            `json:"partition"`
	Acks      string            `json:"acks"`
}

func produce(ctx context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
	var cfg produceConfig
	if err := c.Decode(&cfg); err != nil {
		return nil, err
	}
	s := c.Session.(*session)
	manual := cfg.Partition != nil
	pk := cfg.key() + "|" + cfg.Acks + fmt.Sprintf("|%t", manual)
	cl := s.producers[pk]
	if cl == nil {
		opts := append(cfg.opts(), kgo.ProducerLinger(0), kgo.AllowAutoTopicCreation())
		switch cfg.Acks {
		case "leader":
			opts = append(opts, kgo.RequiredAcks(kgo.LeaderAck()), kgo.DisableIdempotentWrite())
		case "none":
			opts = append(opts, kgo.RequiredAcks(kgo.NoAck()), kgo.DisableIdempotentWrite())
		}
		if manual {
			opts = append(opts, kgo.RecordPartitioner(kgo.ManualPartitioner()))
		}
		var err error
		if cl, err = kgo.NewClient(opts...); err != nil {
			return nil, pluginsdk.Fail("invalid config", err)
		}
		s.producers[pk] = cl
	}
	rec := &kgo.Record{Topic: cfg.Topic, Value: []byte(cfg.Value)}
	if cfg.Key != nil {
		rec.Key = []byte(*cfg.Key)
	}
	if manual {
		rec.Partition = *cfg.Partition
	}
	keys := make([]string, 0, len(cfg.Headers))
	for k := range cfg.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rec.Headers = append(rec.Headers, kgo.RecordHeader{Key: k, Value: []byte(cfg.Headers[k])})
	}
	if c.Traceparent != "" && cfg.Headers["traceparent"] == "" {
		rec.Headers = append(rec.Headers, kgo.RecordHeader{Key: "traceparent", Value: []byte(c.Traceparent)})
	}
	start := time.Now()
	r, err := cl.ProduceSync(ctx, rec).First()
	lat := time.Since(start)
	if err != nil {
		return &pluginsdk.Result{Latency: lat}, classify(err)
	}
	out := int64(len(rec.Key) + len(rec.Value))
	for _, h := range rec.Headers {
		out += int64(len(h.Key) + len(h.Value))
	}
	return &pluginsdk.Result{
		Latency: lat, BytesOut: out,
		Values: map[string]any{"topic": r.Topic, "partition": r.Partition, "offset": r.Offset},
	}, nil
}

type consumeConfig struct {
	connConfig
	Topic     string  `json:"topic"`
	Group     string  `json:"group"`
	Partition *int32  `json:"partition"`
	Offset    *int64  `json:"offset"`
	From      string  `json:"from"`
	Count     int     `json:"count"`
	Key       *string `json:"key"`
	Match     string  `json:"match"`
	Latency   string  `json:"latency"`
}

func (cfg *consumeConfig) startOffset() kgo.Offset {
	if cfg.From == "start" {
		return kgo.NewOffset().AtStart()
	}
	return kgo.NewOffset().AtEnd()
}

func (s *session) consumer(cfg *consumeConfig) (*consumer, error) {
	ck := cfg.key() + "|" + cfg.Topic + "|" + cfg.Group + "|" + cfg.From
	if cfg.Partition != nil {
		ck += "|direct"
	}
	if c := s.consumers[ck]; c != nil {
		return c, nil
	}
	opts := append(cfg.opts(), kgo.FetchMaxWait(100*time.Millisecond), kgo.ConsumeResetOffset(cfg.startOffset()))
	if cfg.Partition != nil {
		off := cfg.startOffset()
		if cfg.Offset != nil {
			off = kgo.NewOffset().At(*cfg.Offset)
		}
		opts = append(opts, kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{cfg.Topic: {*cfg.Partition: off}}))
	} else {
		opts = append(opts, kgo.ConsumeTopics(cfg.Topic))
		if cfg.Group != "" {
			opts = append(opts, kgo.ConsumerGroup(cfg.Group))
		}
	}
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, pluginsdk.Fail("invalid config", err)
	}
	c := &consumer{client: cl, offsets: map[int32]int64{}}
	if cfg.Partition != nil {
		c.offsets[*cfg.Partition] = -1
		if cfg.Offset != nil {
			c.offsets[*cfg.Partition] = *cfg.Offset
		}
	}
	s.consumers[ck] = c
	return c, nil
}

// seek makes a direct consumer read partition p, from off when given;
// a partition already being read at that position is left alone.
// offsets[p] is -1 while the position is not known.
func (c *consumer) seek(topic string, p int32, off *int64, from kgo.Offset) {
	cur, known := c.offsets[p]
	switch {
	case !known && off == nil:
		c.client.AddConsumePartitions(map[string]map[int32]kgo.Offset{topic: {p: from}})
		c.offsets[p] = -1
		return
	case !known:
	case off == nil || *off == cur:
		return
	default:
		c.client.RemoveConsumePartitions(map[string][]int32{topic: {p}})
	}
	c.client.AddConsumePartitions(map[string]map[int32]kgo.Offset{topic: {p: kgo.NewOffset().At(*off)}})
	kept := c.pending[:0]
	for _, r := range c.pending {
		if r.Partition != p {
			kept = append(kept, r)
		}
	}
	c.pending = kept
	c.offsets[p] = *off
}

func consume(ctx context.Context, call *pluginsdk.Call) (*pluginsdk.Result, error) {
	var cfg consumeConfig
	if err := call.Decode(&cfg); err != nil {
		return nil, err
	}
	if cfg.Offset != nil && cfg.Partition == nil {
		return nil, pluginsdk.Failf("invalid config", "offset needs partition")
	}
	var re *regexp.Regexp
	if cfg.Match != "" {
		var err error
		if re, err = regexp.Compile(cfg.Match); err != nil {
			return nil, pluginsdk.Fail("invalid config", err)
		}
	}
	if cfg.Count <= 0 {
		cfg.Count = 1
	}
	s := call.Session.(*session)
	c, err := s.consumer(&cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Partition != nil {
		c.seek(cfg.Topic, *cfg.Partition, cfg.Offset, cfg.startOffset())
	}
	matches := func(r *kgo.Record) bool {
		if r.Topic != cfg.Topic || cfg.Partition != nil && r.Partition != *cfg.Partition {
			return false
		}
		if cfg.Key != nil && string(r.Key) != *cfg.Key {
			return false
		}
		return re == nil || re.Match(r.Value)
	}

	start := time.Now()
	var got []*kgo.Record
	var firstAt, lastAt time.Time
	var bytesIn int64
	take := func(recs []*kgo.Record) []*kgo.Record {
		var rest []*kgo.Record
		for _, r := range recs {
			if cfg.Partition != nil && r.Partition == *cfg.Partition {
				c.offsets[r.Partition] = r.Offset + 1
			}
			if len(got) < cfg.Count && matches(r) {
				got = append(got, r)
				bytesIn += int64(len(r.Key) + len(r.Value))
				now := time.Now()
				if firstAt.IsZero() {
					firstAt = now
				}
				lastAt = now
				continue
			}
			if len(got) >= cfg.Count && r.Topic == cfg.Topic {
				rest = append(rest, r) // keep for the next step
			}
		}
		return rest
	}
	c.pending = take(c.pending)
	for len(got) < cfg.Count {
		fs := c.client.PollRecords(ctx, 500)
		if ctx.Err() != nil {
			break
		}
		var ferr error
		fs.EachError(func(_ string, _ int32, err error) {
			if ferr == nil {
				ferr = err
			}
		})
		if ferr != nil && !errors.Is(ferr, context.DeadlineExceeded) {
			return &pluginsdk.Result{Latency: time.Since(start)}, classify(ferr)
		}
		c.pending = append(c.pending, take(fs.Records())...)
	}
	if len(got) < cfg.Count {
		return &pluginsdk.Result{Latency: time.Since(start), BytesIn: bytesIn, Events: int64(len(got))},
			pluginsdk.Failf("kafka consume timeout", "%d of %d records arrived", len(got), cfg.Count)
	}

	last := got[len(got)-1]
	var maxAge time.Duration
	for _, r := range got {
		if a := time.Since(r.Timestamp); a > maxAge {
			maxAge = a
		}
	}
	res := &pluginsdk.Result{Latency: lastAt.Sub(start), BytesIn: bytesIn}
	if cfg.Latency == "age" {
		res.Latency = max(lastAt.Sub(last.Timestamp), 0)
	}
	if cfg.Count > 1 {
		res.Events = int64(len(got))
		res.Phases.FirstEvent = min(firstAt.Sub(start), res.Latency)
	}
	vals := map[string]any{
		"count": len(got), "topic": last.Topic, "partition": last.Partition, "offset": last.Offset,
		"key": string(last.Key), "maxAgeMs": float64(maxAge.Microseconds()) / 1000,
		"timestamp": last.Timestamp.UnixMilli(),
	}
	if utf8.Valid(last.Value) {
		vals["value"] = string(last.Value)
	}
	var j any
	if json.Unmarshal(last.Value, &j) == nil {
		vals["json"] = j
	}
	if len(last.Headers) > 0 {
		h := map[string]string{}
		for _, x := range last.Headers {
			h[x.Key] = string(x.Value)
		}
		vals["headers"] = h
	}
	res.Values = vals
	return res, nil
}

func classify(err error) error {
	var ke *kerr.Error
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return pluginsdk.Fail("kafka timeout", err)
	case errors.As(err, &ke):
		return pluginsdk.Fail("kafka "+ke.Message, err)
	case errors.As(err, &ne):
		return pluginsdk.Fail("kafka connection error", err)
	}
	return pluginsdk.Fail("kafka error", err)
}
