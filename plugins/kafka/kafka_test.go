package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"

	pluginv1 "github.com/Ivan825/Stampede/gen/stampede/plugin/v1"
	"github.com/Ivan825/Stampede/internal/pluginhost/plugintest/runtest"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/pkg/pluginsdk"
	"github.com/Ivan825/Stampede/pkg/pluginsdk/conformance"
)

func cluster(t *testing.T, partitions int32, topics ...string) []string {
	t.Helper()
	c, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(partitions, topics...))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c.ListenAddrs()
}

type harness struct {
	t   *testing.T
	srv pluginv1.PluginServiceServer
	ses string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	srv, err := pluginsdk.NewServer(newPlugin())
	if err != nil {
		t.Fatal(err)
	}
	o, err := srv.Open(context.Background(), &pluginv1.OpenRequest{Vu: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = srv.Close(context.Background(), &pluginv1.CloseRequest{Session: o.GetSession()}) })
	return &harness{t: t, srv: srv, ses: o.GetSession()}
}

func (h *harness) run(step string, cfg map[string]any, timeout time.Duration) (*pluginv1.ExecuteResponse, map[string]any) {
	h.t.Helper()
	b, _ := json.Marshal(cfg)
	r, err := h.srv.Execute(context.Background(), &pluginv1.ExecuteRequest{Session: h.ses, Step: step, Config: b, TimeoutNs: int64(timeout), Traceparent: "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"})
	if err != nil {
		h.t.Fatal(err)
	}
	var vals map[string]any
	_ = json.Unmarshal(r.GetValues(), &vals)
	return r, vals
}

func TestProduceAndConsume(t *testing.T) {
	brokers := cluster(t, 3, "orders")
	h := newHarness(t)
	conn := func(m map[string]any) map[string]any {
		m["brokers"] = brokers
		m["topic"] = "orders"
		return m
	}

	r, v := h.run("produce", conn(map[string]any{"key": "o-1", "value": `{"id": 1}`, "headers": map[string]any{"source": "test"}}), 5*time.Second)
	if !r.GetOk() || v["offset"] != float64(0) || r.GetBytesOut() == 0 {
		t.Fatalf("produce: %+v %v", r, v)
	}
	part := v["partition"]

	// Read our own write straight from its partition and offset.
	r, v = h.run("consume", conn(map[string]any{"partition": part, "offset": 0, "latency": "age"}), 5*time.Second)
	if !r.GetOk() || v["key"] != "o-1" || v["value"] != `{"id": 1}` {
		t.Fatalf("consume partition: %+v %v", r, v)
	}
	hdrs, _ := v["headers"].(map[string]any)
	if hdrs["source"] != "test" || hdrs["traceparent"] == nil {
		t.Fatalf("headers %v", v["headers"])
	}
	if j, _ := v["json"].(map[string]any); j["id"] != float64(1) {
		t.Fatalf("json %v", v["json"])
	}

	// Seek back and read it again.
	if r, v := h.run("consume", conn(map[string]any{"partition": part, "offset": 0}), 5*time.Second); !r.GetOk() || v["offset"] != float64(0) {
		t.Fatalf("seek: %+v %v", r, v)
	}

	// Several records into one partition, consumed with a filter.
	for i, key := range []string{"a", "b", "a", "a"} {
		r, _ := h.run("produce", conn(map[string]any{"key": key, "value": "v" + string(rune('0'+i)), "partition": 2, "acks": "leader"}), 5*time.Second)
		if !r.GetOk() {
			t.Fatalf("produce %d: %+v", i, r)
		}
	}
	r, v = h.run("consume", conn(map[string]any{"partition": 2, "from": "start", "key": "a", "count": 3}), 5*time.Second)
	if !r.GetOk() || v["count"] != float64(3) || v["value"] != "v3" || r.GetEvents() != 3 {
		t.Fatalf("consume 3: %+v %v", r, v)
	}

	// A consumer group member starting from the beginning sees every record.
	r, v = h.run("consume", conn(map[string]any{"group": "g1", "from": "start", "count": 5}), 10*time.Second)
	if !r.GetOk() || v["count"] != float64(5) {
		t.Fatalf("group: %+v %v", r, v)
	}

	start := time.Now()
	r, _ = h.run("consume", conn(map[string]any{"group": "g1", "match": "never"}), 300*time.Millisecond)
	if r.GetErrorClass() != "kafka consume timeout" || time.Since(start) > 2*time.Second {
		t.Fatalf("timeout: %+v", r)
	}
	if r, _ := h.run("consume", conn(map[string]any{"offset": 3}), time.Second); r.GetErrorClass() != "invalid config" {
		t.Fatalf("offset without partition: %+v", r)
	}
	if r, _ := h.run("consume", conn(map[string]any{"group": "g", "partition": 1}), time.Second); r.GetErrorClass() != "invalid config" {
		t.Fatalf("group and partition: %+v", r)
	}
}

func TestProduceErrors(t *testing.T) {
	brokers := cluster(t, 1, "t")
	h := newHarness(t)
	r, _ := h.run("produce", map[string]any{"brokers": brokers, "topic": "t", "value": "x", "partition": 7}, 2*time.Second)
	if r.GetOk() || r.GetErrorClass() == "" {
		t.Fatalf("missing partition: %+v", r)
	}
	r, _ = h.run("produce", map[string]any{"brokers": []string{"127.0.0.1:1"}, "topic": "t", "value": "x"}, 500*time.Millisecond)
	if r.GetOk() || (r.GetErrorClass() != "kafka timeout" && r.GetErrorClass() != "kafka connection error") {
		t.Fatalf("no broker: %+v", r)
	}
}

func TestExample(t *testing.T) {
	brokers := cluster(t, 4, "orders")
	bin := conformance.Build(t, ".", "kafka")
	res := runtest.Run(t, "examples/orders.yaml", filepath.Dir(bin), map[string]string{"KAFKA_BROKERS": brokers[0]},
		&scenario.Load{VUs: 8, Iterations: 80})
	for _, name := range []string{"place order", "order visible"} {
		st := res.Step(t, name)
		if st.Requests != 80 || st.Failed != 0 || st.Protocols["kafka"] != 80 {
			t.Errorf("%s: %d requests, %d failed (%v)", name, st.Requests, st.Failed, st.Errors)
		}
	}
}

func TestConformance(t *testing.T) {
	brokers := cluster(t, 2, "conf")
	conformance.Run(t, conformance.Build(t, ".", "kafka"), conformance.Options{
		Cases: []conformance.Case{
			{Step: "produce", Config: map[string]any{"brokers": brokers, "topic": "conf", "key": "k", "value": "v", "partition": 0}},
			{Step: "consume", Config: map[string]any{"brokers": brokers, "topic": "conf", "partition": 0, "from": "start"}},
			{Step: "consume", Config: map[string]any{"brokers": brokers, "topic": "conf", "match": "("}, WantClass: "invalid config"},
		},
		VUs: 20,
	})
}
