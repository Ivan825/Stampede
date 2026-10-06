package pipelinelab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

func start(t *testing.T, cfg labkit.Config) (*server, string) {
	t.Helper()
	cfg.Listen = "127.0.0.1:0"
	s, app, err := newServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	return s, srv.URL
}

func client(t *testing.T, s *server, opts ...kgo.Opt) *kgo.Client {
	t.Helper()
	cl, err := kgo.NewClient(append([]kgo.Opt{kgo.SeedBrokers(s.brokers...)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cl.Close)
	return cl
}

// produce writes n orders and returns them by key.
func produce(t *testing.T, s *server, n int) map[string]*kgo.Record {
	t.Helper()
	var recs []*kgo.Record
	for i := range n {
		recs = append(recs, &kgo.Record{Topic: Orders, Key: fmt.Appendf(nil, "order-%d", i),
			Value: fmt.Appendf(nil, `{"order": %d, "customer": %d, "total": 42}`, i, i%Customers+1)})
	}
	if err := client(t, s).ProduceSync(context.Background(), recs...).FirstErr(); err != nil {
		t.Fatal(err)
	}
	out := map[string]*kgo.Record{}
	for _, r := range recs {
		out[string(r.Key)] = r
	}
	return out
}

func waitProcessed(t *testing.T, s *server, n int64) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for s.processed.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("processed %d of %d", s.processed.Load(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func get(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var v map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&v)
	return v
}

func TestEnrichAndLag(t *testing.T) {
	for _, fixes := range []string{"", "all"} {
		s, base := start(t, labkit.Config{Fixes: labkit.ParseFixes(fixes), Fast: true})
		sent := produce(t, s, 12)
		cl := client(t, s, kgo.ConsumeTopics(Enriched), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		got := 0
		for got < len(sent) && ctx.Err() == nil {
			cl.PollFetches(ctx).EachRecord(func(r *kgo.Record) {
				in := sent[string(r.Key)]
				var v map[string]any
				_ = json.Unmarshal(r.Value, &v)
				c, _ := v["customer"].(map[string]any)
				if in == nil || c["name"] == nil || r.Partition != in.Partition || !r.Timestamp.Equal(in.Timestamp) {
					t.Errorf("fixes %q: enriched %s on %d at %v: %s", fixes, r.Key, r.Partition, r.Timestamp, r.Value)
				}
				got++
			})
		}
		cancel()
		if got != len(sent) {
			t.Fatalf("fixes %q: %d of %d enriched", fixes, got, len(sent))
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			l := get(t, base+"/api/consumer-groups/enricher/lag")
			if l["lag"] == 0.0 && len(l["partitions"].([]any)) == Partitions {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("fixes %q: lag %v", fixes, l)
			}
			time.Sleep(50 * time.Millisecond)
		}
		topics := get(t, base+"/api/topics")["topics"].([]any)
		if topics[0].(map[string]any)["records"] != 12.0 || topics[1].(map[string]any)["records"] != 12.0 {
			t.Fatalf("topics %v", topics)
		}
	}
}

func TestParallelBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "parallel"} {
		s, _ := start(t, labkit.Config{Fixes: labkit.ParseFixes(fixes)})
		produce(t, s, 120)
		waitProcessed(t, s, 120)
		peak := s.looking.Peak()
		if fixes == "" && peak != 1 {
			t.Errorf("without the fix records are enriched one at a time: peak %d", peak)
		}
		if fixes != "" && peak < 2 {
			t.Errorf("with the fix partitions are enriched in parallel: peak %d", peak)
		}
	}
}

func TestCommitBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "commit"} {
		s, _ := start(t, labkit.Config{Fixes: labkit.ParseFixes(fixes), Fast: true})
		produce(t, s, 60)
		waitProcessed(t, s, 60)
		time.Sleep(200 * time.Millisecond) // the last commit
		commits := s.commits.Load()
		if fixes == "" && commits != 60 {
			t.Errorf("without the fix every record is committed on its own: %d commits", commits)
		}
		if fixes != "" && (commits == 0 || commits >= 60) {
			t.Errorf("with the fix commits are per batch: %d commits for 60 records", commits)
		}
	}
}
