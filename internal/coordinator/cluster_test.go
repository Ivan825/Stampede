package coordinator_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	workerv1 "github.com/Ivan825/Stampede/gen/stampede/worker/v1"
	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/worker"
)

const token = "test-join-token"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// cluster is a coordinator served over an in-memory gRPC connection with
// in-process workers dialling it.
type cluster struct {
	t     *testing.T
	coord *coordinator.Coordinator
	lis   *bufconn.Listener
	srv   *grpc.Server
	// delay, when set, adds this much one-way latency in each direction
	// on worker connections.
	delay time.Duration

	mu      sync.Mutex
	workers []*testWorker
}

type testWorker struct {
	w      *worker.Worker
	cancel context.CancelFunc
	done   chan error
}

func newCluster(t *testing.T, cfg coordinator.Config) *cluster {
	t.Helper()
	if cfg.JoinToken == "" {
		cfg.JoinToken = token
	}
	if cfg.Logger == nil {
		cfg.Logger = quiet
	}
	return serve(t, coordinator.New(cfg))
}

// serve serves a coordinator on an in-memory listener.
func serve(t *testing.T, coord *coordinator.Coordinator) *cluster {
	t.Helper()
	c := &cluster{t: t, coord: coord, lis: bufconn.Listen(1 << 20)}
	c.srv = grpc.NewServer(coordinator.ServerOptions(nil)...)
	c.coord.Register(c.srv)
	go func() { _ = c.srv.Serve(c.lis) }()
	t.Cleanup(func() {
		c.mu.Lock()
		ws := append([]*testWorker(nil), c.workers...)
		c.mu.Unlock()
		for _, w := range ws {
			w.cancel()
			<-w.done
		}
		c.srv.Stop()
	})
	return c
}

func (c *cluster) dialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			conn, err := c.lis.DialContext(ctx)
			if err != nil || c.delay == 0 {
				return conn, err
			}
			return newLatencyConn(conn, c.delay), nil
		}),
	}
}

// addWorker starts an in-process worker connected to the cluster.
func (c *cluster) addWorker(cfg worker.Config) *testWorker {
	cfg.Server = "passthrough:///bufnet"
	cfg.Insecure = true
	cfg.DialOptions = append(cfg.DialOptions, c.dialOptions()...)
	if cfg.Token == "" {
		cfg.Token = token
	}
	if cfg.Logger == nil {
		cfg.Logger = quiet
	}
	ctx, cancel := context.WithCancel(context.Background())
	tw := &testWorker{w: worker.New(cfg), cancel: cancel, done: make(chan error, 1)}
	go func() { tw.done <- tw.w.Run(ctx) }()
	c.mu.Lock()
	c.workers = append(c.workers, tw)
	c.mu.Unlock()
	return tw
}

// waitConnected waits until n workers are registered and connected.
func (c *cluster) waitConnected(n int, within time.Duration) {
	c.t.Helper()
	deadline := time.Now().Add(within)
	for {
		got := 0
		for _, w := range c.coord.Workers() {
			if w.Connected {
				got++
			}
		}
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("%d of %d workers connected after %s", got, n, within)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// rawClient opens a Connect stream without the worker package, for
// protocol-level tests.
func (c *cluster) rawClient(ctx context.Context) (workerv1.WorkerService_ConnectClient, func()) {
	c.t.Helper()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		append(c.dialOptions(), grpc.WithTransportCredentials(insecure.NewCredentials()))...)
	if err != nil {
		c.t.Fatal(err)
	}
	stream, err := workerv1.NewWorkerServiceClient(conn).Connect(ctx)
	if err != nil {
		c.t.Fatal(err)
	}
	return stream, func() { _ = conn.Close() }
}

// latencyConn adds one-way latency in both directions without limiting
// throughput: bytes are delivered delay after they were sent, and
// back-to-back messages travel pipelined, as on a real long link.
type latencyConn struct {
	net.Conn
	delay  time.Duration
	out    chan chunk
	in     chan chunk
	done   chan struct{}
	once   sync.Once
	rest   []byte
	readMu sync.Mutex
}

type chunk struct {
	b   []byte
	due time.Time
}

func newLatencyConn(c net.Conn, d time.Duration) *latencyConn {
	l := &latencyConn{Conn: c, delay: d, out: make(chan chunk, 1024), in: make(chan chunk, 1024), done: make(chan struct{})}
	go func() {
		for {
			select {
			case <-l.done:
				return
			case ch := <-l.out:
				time.Sleep(time.Until(ch.due))
				if _, err := l.Conn.Write(ch.b); err != nil {
					return
				}
			}
		}
	}()
	go func() {
		defer close(l.in)
		for {
			b := make([]byte, 32<<10)
			n, err := l.Conn.Read(b)
			if n > 0 {
				select {
				case l.in <- chunk{b: b[:n], due: time.Now().Add(d)}:
				case <-l.done:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return l
}

func (l *latencyConn) Write(b []byte) (int, error) {
	cp := append([]byte(nil), b...)
	select {
	case l.out <- chunk{b: cp, due: time.Now().Add(l.delay)}:
		return len(b), nil
	case <-l.done:
		return 0, net.ErrClosed
	}
}

func (l *latencyConn) Read(b []byte) (int, error) {
	l.readMu.Lock()
	defer l.readMu.Unlock()
	if len(l.rest) == 0 {
		ch, ok := <-l.in
		if !ok {
			return 0, io.EOF
		}
		time.Sleep(time.Until(ch.due))
		l.rest = ch.b
	}
	n := copy(b, l.rest)
	l.rest = l.rest[n:]
	return n, nil
}

func (l *latencyConn) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.Conn.Close()
}

// collect drains a run's live streams and waits for its result.
type collected struct {
	res    *coordinator.Result
	err    error
	live   []*metrics.Snapshot
	events []coordinator.RunEvent
}

func collect(t *testing.T, r *coordinator.Run, timeout time.Duration) collected {
	t.Helper()
	var out collected
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for s := range r.Snapshots() {
			out.live = append(out.live, s)
		}
	}()
	go func() {
		defer wg.Done()
		for e := range r.Events() {
			out.events = append(out.events, e)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out.res, out.err = r.Wait(ctx)
	if ctx.Err() != nil {
		t.Fatalf("run did not finish within %s", timeout)
	}
	wg.Wait()
	return out
}

func (c collected) eventsOf(typ coordinator.EventType) []coordinator.RunEvent {
	var out []coordinator.RunEvent
	for _, e := range c.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func started(snaps []*metrics.Snapshot) uint64 {
	var n uint64
	for _, s := range snaps {
		for _, j := range s.Journeys {
			n += j.Started
		}
	}
	return n
}

// expectedArrivals counts the arrivals among the first total that the
// engine assigns to share [lo, hi): arrival k belongs to the share that
// contains frac(k*phi). This is the engine's own rule, so a worker's count
// is known exactly in advance.
func expectedArrivals(total int, lo, hi float64) uint64 {
	if lo == 0 && hi == 1 {
		return uint64(total)
	}
	var n uint64
	for k := 0; k < total; k++ {
		f := math.Mod(float64(k)*0.6180339887498949, 1)
		if f >= lo && f < hi {
			n++
		}
	}
	return n
}

func rateScenario(url string, rate, seconds int) []byte {
	return []byte(fmt.Sprintf(`
metadata: {name: distributed}
target: {baseURL: %q}
journeys: [{name: a, steps: [{get: /}]}]
load: {mode: rate, rate: %d/s, duration: %ds, gracefulStop: 5s}`, url, rate, seconds))
}
