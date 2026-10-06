package coordinator_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workerv1 "github.com/Ivan825/Stampede/gen/stampede/worker/v1"
	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/wire"
	"github.com/Ivan825/Stampede/internal/worker"
)

const tinyScenario = `
metadata: {name: tiny}
target: {baseURL: "http://127.0.0.1:1"}
journeys: [{name: a, steps: [{get: /}]}]
load: {mode: rate, rate: 10/s, duration: 2s}`

// fake speaks the protocol by hand so tests control exactly what the
// coordinator receives.
type fake struct {
	t      *testing.T
	stream workerv1.WorkerService_ConnectClient
	close  func()
	cancel context.CancelFunc
	sendMu sync.Mutex
	id     string
	starts chan *workerv1.StartRun
	stops  chan string
	// refuse answers StartRun with RunFailed.
	refuse string
}

func (c *cluster) fake(name, resume, activeRun string, refuse string) *fake {
	c.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stream, closeConn := c.rawClient(ctx)
	f := &fake{t: c.t, stream: stream, close: closeConn, cancel: cancel, starts: make(chan *workerv1.StartRun, 4), stops: make(chan string, 4), refuse: refuse}
	f.send(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_Hello{Hello: &workerv1.Hello{
		Protocol: wire.Version(), JoinToken: token, Name: name,
		Capacity: &workerv1.Capacity{Cpus: 1}, ResumeWorkerId: resume, ActiveRunId: activeRun,
	}}})
	m, err := stream.Recv()
	if err != nil {
		c.t.Fatal(err)
	}
	f.id = m.GetWelcome().GetWorkerId()
	go f.serve()
	return f
}

func (f *fake) send(m *workerv1.WorkerMessage) {
	f.sendMu.Lock()
	defer f.sendMu.Unlock()
	_ = f.stream.Send(m)
}

func (f *fake) serve() {
	for {
		m, err := f.stream.Recv()
		if err != nil {
			return
		}
		switch msg := m.GetMsg().(type) {
		case *workerv1.ServerMessage_ClockPing:
			now := time.Now().UnixNano()
			f.send(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_ClockPong{ClockPong: &workerv1.ClockPong{
				T1UnixNano: msg.ClockPing.GetT1UnixNano(), T2UnixNano: now, T3UnixNano: now,
			}}})
		case *workerv1.ServerMessage_StartRun:
			id := msg.StartRun.GetRunId()
			if f.refuse != "" {
				f.send(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_RunFailed{RunFailed: &workerv1.RunFailed{RunId: id, Error: f.refuse}}})
			} else {
				f.send(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_RunAccepted{RunAccepted: &workerv1.RunAccepted{RunId: id}}})
			}
			f.starts <- msg.StartRun
		case *workerv1.ServerMessage_StopRun:
			select {
			case f.stops <- msg.StopRun.GetRunId():
			default:
			}
		}
	}
}

func (f *fake) snapshot(runID string, interval int64, seq uint64, requests uint64) {
	s := metrics.NewSnapshot(interval)
	s.Seq = seq
	s.Step(0).Requests = requests
	s.Journey(0).Started = requests
	p, err := wire.SnapshotToProto(runID, s, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	f.send(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_Snapshot{Snapshot: p}})
}

func (f *fake) disconnect() {
	f.cancel()
	f.close()
}

func TestResentSnapshotsAfterReconnectCountOnce(t *testing.T) {
	c := newCluster(t, coordinator.Config{})
	f := c.fake("fake", "", "", "")
	c.waitConnected(1, 5*time.Second)

	type started struct {
		r   *coordinator.Run
		err error
	}
	ch := make(chan started, 1)
	go func() {
		r, err := c.coord.Start(context.Background(), coordinator.RunSpec{ID: "resend", Scenario: []byte(tinyScenario), StartDelay: 300 * time.Millisecond})
		ch <- started{r, err}
	}()
	<-f.starts
	st := <-ch
	if st.err != nil {
		t.Fatal(st.err)
	}

	f.snapshot("resend", 0, 1, 10)
	f.snapshot("resend", 0, 1, 10) // resent on the same stream
	time.Sleep(50 * time.Millisecond)
	f.disconnect()

	// The worker comes back with its id and resends everything it holds
	// that was not acknowledged, plus new data.
	g := c.fake("fake", f.id, "resend", "")
	if g.id != f.id {
		t.Fatalf("reconnected as %s, want %s", g.id, f.id)
	}
	g.snapshot("resend", 0, 1, 10)
	g.snapshot("resend", 1, 2, 5)
	g.send(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_RunFinished{RunFinished: &workerv1.RunFinished{
		RunId: "resend", StopReason: "completed", LastSeq: 2,
	}}})

	out := collect(t, st.r, 10*time.Second)
	if out.err != nil {
		t.Fatal(out.err)
	}
	var reqs uint64
	for _, s := range out.res.Snapshots {
		reqs += s.Totals().Requests
	}
	if reqs != 15 {
		t.Errorf("merged %d requests, want 15 (10 + 5, resends ignored)", reqs)
	}
	ws := out.res.Workers[0]
	if ws.State != coordinator.WorkerFinished || ws.Requests != 15 || ws.Snapshots != 2 || out.res.Degraded {
		t.Errorf("summary %+v degraded %v", ws, out.res.Degraded)
	}
	g.disconnect()
}

func TestRegistrationRejections(t *testing.T) {
	c := newCluster(t, coordinator.Config{})

	t.Run("wrong token", func(t *testing.T) {
		tw := c.addWorker(worker.Config{Name: "intruder", Token: "nope"})
		select {
		case err := <-tw.done:
			tw.done <- err // for cleanup
			if status.Code(err) != codes.Unauthenticated {
				t.Errorf("got %v, want Unauthenticated", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("worker with a wrong token kept retrying")
		}
	})

	hello := func(v *workerv1.ProtocolVersion, tok string) error {
		stream, closeConn := c.rawClient(context.Background())
		defer closeConn()
		_ = stream.Send(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_Hello{Hello: &workerv1.Hello{Protocol: v, JoinToken: tok, Name: "x"}}})
		_, err := stream.Recv()
		return err
	}
	t.Run("incompatible major", func(t *testing.T) {
		err := hello(&workerv1.ProtocolVersion{Major: 2, Minor: 1}, token)
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "worker speaks protocol v2.1 but this server speaks v1.1") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("newer minor accepted", func(t *testing.T) {
		if err := hello(&workerv1.ProtocolVersion{Major: 1, Minor: 9}, token); err != nil {
			t.Errorf("got %v", err)
		}
	})
	t.Run("hello first", func(t *testing.T) {
		stream, closeConn := c.rawClient(context.Background())
		defer closeConn()
		_ = stream.Send(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_Heartbeat{Heartbeat: &workerv1.Heartbeat{}}})
		if _, err := stream.Recv(); status.Code(err) != codes.InvalidArgument {
			t.Errorf("got %v", err)
		}
	})
	t.Run("no token configured", func(t *testing.T) {
		c2 := serve(t, coordinator.New(coordinator.Config{Logger: quiet}))
		stream, closeConn := c2.rawClient(context.Background())
		defer closeConn()
		_ = stream.Send(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_Hello{Hello: &workerv1.Hello{Protocol: wire.Version(), Name: "x"}}})
		if _, err := stream.Recv(); status.Code(err) != codes.Unauthenticated || !strings.Contains(err.Error(), "no join token") {
			t.Errorf("got %v", err)
		}
	})
}

func TestWorkersListing(t *testing.T) {
	c := newCluster(t, coordinator.Config{HeartbeatInterval: 50 * time.Millisecond})
	tw := c.addWorker(worker.Config{
		Name: "mum-1", Region: "mumbai", Labels: map[string]string{"pool": "a"}, CPUs: 4, MaxVUs: 500,
		Clock: func() time.Time { return time.Now().Add(-90 * time.Second) },
	})
	c.addWorker(worker.Config{Name: "fra-1", Labels: map[string]string{"region": "frankfurt"}, CPUs: 2})
	c.waitConnected(2, 5*time.Second)

	deadline := time.Now().Add(5 * time.Second)
	var ws []coordinator.WorkerInfo
	for {
		ws = c.coord.Workers()
		ready := len(ws) == 2
		for _, w := range ws {
			ready = ready && !w.LastHeartbeat.IsZero() && w.ClockOffset != 0
		}
		if ready || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	byName := map[string]coordinator.WorkerInfo{}
	for _, w := range ws {
		byName[w.Name] = w
	}
	m := byName["mum-1"]
	if m.Region != "mumbai" || m.Labels["pool"] != "a" || m.Capacity.CPUs != 4 || m.Capacity.MaxVUs != 500 ||
		!m.Connected || m.LastHeartbeat.IsZero() || m.ConnectedSince.IsZero() || len(m.Capacity.Protocols) == 0 {
		t.Errorf("mum-1 = %+v", m)
	}
	if d := (m.ClockOffset + 90*time.Second).Abs(); d > 5*time.Millisecond {
		t.Errorf("clock offset %v, want -90s", m.ClockOffset)
	}
	if f := byName["fra-1"]; f.Region != "frankfurt" {
		t.Errorf("region from label: %+v", f)
	}

	// A worker that goes away between runs is forgotten.
	tw.cancel()
	deadline = time.Now().Add(5 * time.Second)
	for len(c.coord.Workers()) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("workers %+v", c.coord.Workers())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStartErrors(t *testing.T) {
	c := newCluster(t, coordinator.Config{})
	c.addWorker(worker.Config{Name: "a", MaxVUs: 5})
	c.addWorker(worker.Config{Name: "b", MaxVUs: 5})
	c.waitConnected(2, 5*time.Second)
	ctx := context.Background()

	tests := []struct {
		name string
		spec coordinator.RunSpec
		want string
	}{
		{"no id", coordinator.RunSpec{Scenario: []byte(tinyScenario)}, "run id"},
		{"bad scenario", coordinator.RunSpec{ID: "x", Scenario: []byte("load: {vus: nope}")}, ""},
		{"too many workers", coordinator.RunSpec{ID: "x", Scenario: []byte(tinyScenario), Workers: 3}, "not enough capacity"},
		{"missing region", coordinator.RunSpec{ID: "x", Scenario: []byte(tinyScenario), Regions: map[string]float64{"mars": 1}}, "no idle workers in region mars"},
		{"too many users", coordinator.RunSpec{ID: "x", Scenario: []byte(strings.Replace(tinyScenario, "rate: 10/s,", "rate: 10/s, maxVUs: 20,", 1))}, "accepts 5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.coord.Start(ctx, tt.spec)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
	for _, w := range c.coord.Workers() {
		if w.CurrentRun != "" {
			t.Errorf("failed start left %s on run %s", w.Name, w.CurrentRun)
		}
	}
}

func TestStartFailsWhenAWorkerRefuses(t *testing.T) {
	c := newCluster(t, coordinator.Config{})
	ok := c.fake("good", "", "", "")
	bad := c.fake("bad", "", "", "disk full")
	defer ok.disconnect()
	defer bad.disconnect()
	c.waitConnected(2, 5*time.Second)
	_, err := c.coord.Start(context.Background(), coordinator.RunSpec{ID: "refused", Scenario: []byte(tinyScenario), StartDelay: 500 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "bad refused the run: disk full") {
		t.Fatalf("err = %v", err)
	}
	for _, w := range c.coord.Workers() {
		if w.CurrentRun != "" {
			t.Errorf("%s still reserved for %s", w.Name, w.CurrentRun)
		}
	}
	// The worker that had accepted is told to stop.
	select {
	case id := <-ok.stops:
		if id != "refused" {
			t.Errorf("stop for %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Error("the accepting worker was not told to stop")
	}
}
