// Package pipelinelab is PipelineLab, an event pipeline: an in-process
// Kafka cluster (franz-go's kfake) with an orders topic, an enricher
// service that consumes it as the "enricher" consumer group, looks up each
// order's customer and writes the result to orders.enriched, and an admin
// API over HTTP that reports topics and consumer lag. It is the reference
// app for the event-pipelines pack.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - parallel: the enricher handles every record of every partition one
//     at a time, so its throughput is one customer lookup at a time and a
//     burst turns into lag (fix "parallel" works on each partition's
//     records concurrently, keeping order within a partition).
//   - commit: the enricher commits its offset after every record, a round
//     trip to the group coordinator each (fix "commit" commits once per
//     batch it polls).
package pipelinelab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

const (
	// Orders is the topic the pipeline consumes.
	Orders = "orders"
	// Enriched is the topic it writes to: same key, same partition, same
	// timestamp as the order, so a record's age there is the pipeline's
	// end-to-end delay.
	Enriched = "orders.enriched"
	// Group is the enricher's consumer group.
	Group = "enricher"
	// Partitions is each topic's partition count.
	Partitions = 6
	// Customers is how many customers the lookup knows: 1 to 10000.
	Customers = 10000
)

type server struct {
	cfg     labkit.Config
	cluster *kfake.Cluster
	brokers []string
	client  *kgo.Client // the enricher: group member and producer
	admin   *kadm.Client
	cancel  context.CancelFunc
	stopped chan struct{}

	lookup    time.Duration
	looking   labkit.Gauge
	processed atomic.Int64
	commits   atomic.Int64
	failed    atomic.Int64
}

// Open starts PipelineLab's Kafka cluster on cfg.Listen and its enricher,
// and returns the app.
func Open(cfg labkit.Config) (*labkit.App, error) {
	_, app, err := newServer(cfg)
	return app, err
}

func newServer(cfg labkit.Config) (*server, *labkit.App, error) {
	s := &server{cfg: cfg, lookup: 4 * time.Millisecond, stopped: make(chan struct{})}
	if cfg.Fast {
		s.lookup = 200 * time.Microsecond
	}
	opts := []kfake.Opt{kfake.NumBrokers(1), kfake.SeedTopics(Partitions, Orders, Enriched)}
	if cfg.Listen != "" {
		_, p, err := net.SplitHostPort(cfg.Listen)
		if err != nil {
			return nil, nil, fmt.Errorf("listen address %q: %w", cfg.Listen, err)
		}
		if port, _ := strconv.Atoi(p); port > 0 {
			opts = append(opts, kfake.Ports(port))
		}
	}
	c, err := kfake.NewCluster(opts...)
	if err != nil {
		return nil, nil, err
	}
	s.cluster, s.brokers = c, c.ListenAddrs()
	s.client, err = kgo.NewClient(
		kgo.SeedBrokers(s.brokers...),
		kgo.ConsumerGroup(Group), kgo.ConsumeTopics(Orders),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll(),
		kgo.FetchMaxWait(100*time.Millisecond),
		kgo.RecordPartitioner(kgo.ManualPartitioner()),
		kgo.ProducerLinger(0),
	)
	if err != nil {
		c.Close()
		return nil, nil, err
	}
	adm, err := kgo.NewClient(kgo.SeedBrokers(s.brokers...))
	if err != nil {
		s.client.Close()
		c.Close()
		return nil, nil, err
	}
	s.admin = kadm.NewClient(adm)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.enrich(ctx)

	routes := []labkit.Route{
		{Method: "GET", Path: "/api/topics", Tag: "topics", Summary: "Topics with their partitions and log-end offsets"},
		{Method: "GET", Path: "/api/consumer-groups", Tag: "consumer-groups", Summary: "Consumer groups and their total lag"},
		{Method: "GET", Path: "/api/consumer-groups/{group}/lag", Tag: "consumer-groups", Summary: "A group's lag per partition"},
		{Method: "GET", Path: "/api/pipeline", Tag: "pipelines", Summary: "Records the enricher has processed and committed"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "PipelineLab", "brokers": s.brokers,
			"links": map[string]string{"openapi": "/openapi.json", "lag": "/api/consumer-groups/" + Group + "/lag"}})
	})
	mux.Handle("GET /openapi.json", labkit.OpenAPI("PipelineLab", "A Kafka event pipeline with an enricher consumer group, with planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/topics", s.topics)
	mux.HandleFunc("GET /api/consumer-groups", s.groups)
	mux.HandleFunc("GET /api/consumer-groups/{group}/lag", s.lag)
	mux.HandleFunc("GET /api/pipeline", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"processed": s.processed.Load(), "commits": s.commits.Load(), "failed": s.failed.Load()})
	})
	return s, &labkit.App{
		Handler: mux,
		Env:     map[string]string{"KAFKA_BROKERS": s.brokers[0]},
		Close: func() {
			cancel()
			<-s.stopped
			s.client.CloseAllowingRebalance()
			adm.Close()
			c.Close()
		},
	}, nil
}

// enrich is the enricher's poll loop.
func (s *server) enrich(ctx context.Context) {
	defer close(s.stopped)
	for {
		fs := s.client.PollFetches(ctx)
		if ctx.Err() != nil {
			return
		}
		if fs.Empty() {
			s.client.AllowRebalance()
			continue
		}
		if s.cfg.Fixes.On("parallel") {
			var wg sync.WaitGroup
			fs.EachPartition(func(p kgo.FetchTopicPartition) {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for _, r := range p.Records {
						s.handle(ctx, r)
					}
				}()
			})
			wg.Wait()
		} else {
			// Bottleneck: one record at a time, whatever its partition.
			fs.EachRecord(func(r *kgo.Record) { s.handle(ctx, r) })
		}
		if s.cfg.Fixes.On("commit") {
			if err := s.client.Flush(ctx); err == nil && s.client.CommitUncommittedOffsets(ctx) == nil {
				s.commits.Add(1)
			}
		}
		s.client.AllowRebalance()
	}
}

// handle enriches one order: look up its customer, write the result to
// orders.enriched and, without the commit fix, commit right away.
func (s *server) handle(ctx context.Context, r *kgo.Record) {
	var order map[string]any
	out := map[string]any{}
	if err := json.Unmarshal(r.Value, &order); err != nil || order == nil {
		out["error"] = "the order is not a JSON object"
	} else {
		id, _ := order["customer"].(float64)
		end := s.looking.Enter()
		time.Sleep(s.lookup) // the customer database
		end()
		for k, v := range order {
			out[k] = v
		}
		if id >= 1 && id <= Customers && id == float64(int(id)) {
			out["customer"] = map[string]any{"id": int(id), "name": fmt.Sprintf("customer-%d", int(id)), "tier": []string{"bronze", "silver", "gold"}[int(id)%3]}
		} else {
			out["customerError"] = "unknown customer"
		}
		out["enrichedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	b, _ := json.Marshal(out)
	rec := &kgo.Record{Topic: Enriched, Partition: r.Partition, Key: r.Key, Value: b, Timestamp: r.Timestamp, Headers: r.Headers}
	s.client.Produce(ctx, rec, func(_ *kgo.Record, err error) {
		if err != nil && !errors.Is(err, context.Canceled) {
			s.failed.Add(1)
		}
	})
	s.processed.Add(1)
	if !s.cfg.Fixes.On("commit") {
		// Bottleneck: a flush and a synchronous commit per record.
		if err := s.client.Flush(ctx); err == nil && s.client.CommitRecords(ctx, r) == nil {
			s.commits.Add(1)
		}
	}
}

func (s *server) topics(w http.ResponseWriter, r *http.Request) {
	ends, err := s.admin.ListEndOffsets(r.Context(), Orders, Enriched)
	if err != nil {
		labkit.Error(w, 502, "kafka_error", err.Error())
		return
	}
	out := []map[string]any{}
	for _, t := range []string{Orders, Enriched} {
		var parts []map[string]any
		var total int64
		for p := range int32(Partitions) {
			o, _ := ends.Lookup(t, p)
			parts = append(parts, map[string]any{"partition": p, "end": o.Offset})
			total += o.Offset
		}
		out = append(out, map[string]any{"name": t, "partitions": parts, "records": total})
	}
	labkit.JSON(w, 200, map[string]any{"topics": out})
}

type partitionLag struct {
	Partition int32 `json:"partition"`
	Committed int64 `json:"committed"`
	End       int64 `json:"end"`
	Lag       int64 `json:"lag"`
}

// groupLag is a group's committed offsets against the log end of each
// partition it consumes.
func (s *server) groupLag(ctx context.Context, group string) (string, int64, []partitionLag, error) {
	lags, err := s.admin.Lag(ctx, group)
	if err != nil {
		return "", 0, nil, err
	}
	l, ok := lags[group]
	if !ok {
		return "", 0, nil, errNoGroup
	}
	if err := l.Error(); err != nil {
		return "", 0, nil, err
	}
	var out []partitionLag
	for _, ps := range l.Lag {
		for _, m := range ps {
			out = append(out, partitionLag{Partition: m.Partition, Committed: max(m.Commit.At, 0), End: m.End.Offset, Lag: m.Lag})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Partition < out[j].Partition })
	return l.State, l.Lag.Total(), out, nil
}

var errNoGroup = errors.New("no such group")

func (s *server) groups(w http.ResponseWriter, r *http.Request) {
	state, total, _, err := s.groupLag(r.Context(), Group)
	if err != nil {
		labkit.Error(w, 502, "kafka_error", err.Error())
		return
	}
	labkit.JSON(w, 200, map[string]any{"groups": []map[string]any{{"group": Group, "state": state, "lag": total}}})
}

func (s *server) lag(w http.ResponseWriter, r *http.Request) {
	group := r.PathValue("group")
	if group != Group {
		labkit.Error(w, 404, "not_found", "no such consumer group")
		return
	}
	state, total, parts, err := s.groupLag(r.Context(), group)
	if err != nil {
		labkit.Error(w, 502, "kafka_error", err.Error())
		return
	}
	labkit.JSON(w, 200, map[string]any{"group": group, "state": state, "lag": total, "partitions": parts})
}
