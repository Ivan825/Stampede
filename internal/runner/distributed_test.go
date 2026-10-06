package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/worker"
)

func TestRunDistributedBuildsReport(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	coord := coordinator.New(coordinator.Config{JoinToken: "t", Logger: quiet})
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(coordinator.ServerOptions(nil)...)
	coord.Register(gs)
	go func() { _ = gs.Serve(lis) }()
	defer gs.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < 2; i++ {
		w := worker.New(worker.Config{
			Server: "passthrough:///bufnet", Token: "t", Insecure: true, Name: fmt.Sprintf("w%d", i), Logger: quiet,
			DialOptions: []grpc.DialOption{grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			})},
		})
		go func() { _ = w.Run(ctx) }()
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(coord.Workers()) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("workers did not connect")
		}
		time.Sleep(5 * time.Millisecond)
	}

	var progress, events atomic.Int64
	rep, res, err := RunDistributed(context.Background(), DistributedOptions{
		Coordinator: coord,
		Spec: coordinator.RunSpec{ID: "dist", StartDelay: 300 * time.Millisecond, Scenario: []byte(fmt.Sprintf(`
metadata: {name: dist}
target: {baseURL: %q}
journeys: [{name: a, steps: [{get: /}]}]
load: {mode: rate, rate: 100/s, duration: 2s}
targets: ["p95 < 1s", "errors < 1%%"]`, srv.URL))},
		Logger:   quiet,
		Progress: func(Progress) { progress.Add(1) },
		OnEvent:  func(coordinator.RunEvent) { events.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Overall.Requests != 200 || hits.Load() != 200 {
		t.Errorf("report has %d requests, server saw %d, want 200", rep.Overall.Requests, hits.Load())
	}
	if rep.Verdict != report.VerdictPass || rep.Load.Workers != 2 || len(rep.Workers) != 2 || rep.Target != srv.URL {
		t.Errorf("verdict %s workers %d rows %d target %q", rep.Verdict, rep.Load.Workers, len(rep.Workers), rep.Target)
	}
	if len(rep.Timeline) < 2 || progress.Load() < 2 || events.Load() < 4 {
		t.Errorf("timeline %d progress %d events %d", len(rep.Timeline), progress.Load(), events.Load())
	}
	if len(res.Workers) != 2 || rep.Workers[0].ShareLo != 0 || rep.Workers[1].ShareHi != 1 {
		t.Errorf("worker rows %+v", rep.Workers)
	}
}

func TestAnnotateExplainsLossAndSaturation(t *testing.T) {
	rep := &report.Report{Verdict: report.VerdictFail}
	Annotate(rep, &coordinator.Result{
		Interval: time.Second,
		Workers: []coordinator.WorkerSummary{
			{Name: "b", Region: "mumbai", ShareLo: 0.5, ShareHi: 1, State: coordinator.WorkerLost, Lost: &coordinator.Window{From: 12, To: 29}},
			{
				Name: "a", ShareLo: 0, ShareHi: 0.5, State: coordinator.WorkerFinished,
				Saturated:         []coordinator.Window{{From: 3, To: 5}, {From: 9, To: 9}},
				SaturationReasons: []string{"cpu", "scheduling lag"},
			},
		},
	})
	if len(rep.Workers) != 2 || rep.Workers[0].Name != "a" {
		t.Fatalf("rows %+v", rep.Workers)
	}
	if l := rep.Workers[1].Lost; l == nil || l.From != 12 || l.To != 30 {
		t.Errorf("lost span %+v", l)
	}
	if s := rep.Workers[0].Saturated; len(s) != 2 || s[0] != (report.Span{From: 3, To: 6}) {
		t.Errorf("saturated spans %+v", s)
	}
	notes := strings.Join(rep.Notes, "\n")
	for _, want := range []string{
		"Worker b (mumbai) was lost at 12s; its 50.0% share",
		"Worker a was saturated for 4s (3s–6s, 9s–10s: cpu, scheduling lag)",
		"generator-limited",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}
	if rep.Verdict != report.VerdictGeneratorLimit {
		t.Errorf("verdict %s", rep.Verdict)
	}
}
