// Package coordinator is the server side of distributed execution. It
// registers workers that dial in over gRPC, splits a run across them,
// starts them at one synchronised instant and merges their per-interval
// snapshots into a single stream.
//
// # Registration
//
// A worker opens one bidirectional Connect stream and sends Hello with the
// server's join token (compared in constant time) and its protocol
// version; an incompatible major version is refused with an explanation.
// The server answers with Welcome, an assigned id and the heartbeat
// interval. Three missed heartbeats mark a worker lost.
//
// # Sharding
//
// Load is split into contiguous slices [lo, hi) of [0, 1) in proportion
// to each worker's CPUs, optionally first by region. The engine assigns
// every open-model arrival and every closed-model user to exactly one
// slice and partitions unique test data by worker index, so the workers
// together run exactly the single-engine plan.
//
// # Clock synchronisation
//
// Before a run the server exchanges several ClockPing/ClockPong round
// trips with each worker and keeps the offset from the one with the lowest
// round trip (NTP style). T0 is sent in each worker's own clock, so a
// worker whose clock is off by seconds still starts within a fraction of
// the round trip of everyone else.
//
// # Worker loss
//
// A lost worker's data up to the loss is kept and the loss is reported as
// an event and as a window in the result. Its share is deliberately not
// moved to other workers: the engine fixes a worker's slice of the arrival
// schedule at T0, and restarting that slice elsewhere mid-run would either
// replay the missed arrivals as a burst or need the schedule to resume at
// an offset. The run continues degraded instead and the report says, for
// which window, how much of the planned load was missing. That keeps the
// numbers honest, which matters more than reaching the planned rate.
//
// # Transport security
//
// Three modes, from strongest:
//
//   - Mutual TLS with the built-in CA (Config.CA, stampede server
//     --worker-mtls). Workers enroll once per certificate lifetime with the
//     Enroll RPC, proving they know the join token without sending it, and
//     then connect with their own certificate. See internal/pki.
//   - TLS with a server certificate from your own CA (ServerOptions);
//     workers verify it with the system roots or a CA they are given, and
//     send the join token in Hello.
//   - No TLS, for trusted networks only: the join token travels in clear.
package coordinator

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	workerv1 "github.com/Ivan825/Stampede/gen/stampede/worker/v1"
	"github.com/Ivan825/Stampede/internal/pki"
	"github.com/Ivan825/Stampede/internal/wire"
)

// Config configures a coordinator.
type Config struct {
	// JoinToken is the secret workers present in Hello. Workers are
	// refused while it is empty.
	JoinToken string
	// HeartbeatInterval is how often workers send heartbeats (default
	// 1s). Three missed heartbeats mark a worker lost.
	HeartbeatInterval time.Duration
	Logger            *slog.Logger

	// ClockSamples is the number of ping/pong round trips per clock
	// synchronisation (default 8).
	ClockSamples int
	// CA, when set, turns on mutual TLS: workers enroll for a certificate
	// with Enroll and must present it on Connect, where it replaces the
	// join token as their credential. The gRPC server must use
	// CA.ServerTLS.
	CA *pki.CA
	// CertValidity is the lifetime of enrolled worker certificates
	// (default DefaultCertValidity).
	CertValidity time.Duration

	// MergeGrace is how long after an interval ends the merged snapshot
	// waits for slow workers before it is emitted without them (default
	// two snapshot intervals).
	MergeGrace time.Duration
}

// Coordinator registers workers and runs distributed tests on them.
type Coordinator struct {
	workerv1.UnimplementedWorkerServiceServer

	cfg Config
	log *slog.Logger

	mu      sync.Mutex
	workers map[string]*workerConn
	runs    map[string]*Run
}

// New returns a coordinator. Register it on a gRPC server to accept
// workers.
func New(cfg Config) *Coordinator {
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.ClockSamples <= 0 {
		cfg.ClockSamples = 8
	}
	return &Coordinator{
		cfg: cfg, log: cfg.Logger,
		workers: map[string]*workerConn{}, runs: map[string]*Run{},
	}
}

// GRPCService returns the WorkerService implementation, for callers that
// register services themselves.
func (c *Coordinator) GRPCService() workerv1.WorkerServiceServer { return c }

// Register adds the WorkerService to s.
func (c *Coordinator) Register(s *grpc.Server) { workerv1.RegisterWorkerServiceServer(s, c) }

// ServerOptions returns the gRPC server options the worker protocol
// expects: keepalives that notice dead connections within seconds, and
// TLS when tlsCfg is not nil.
func ServerOptions(tlsCfg *tls.Config) []grpc.ServerOption {
	opts := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: 15 * time.Second, Timeout: 5 * time.Second}),
		// Workers ping every 10s; allow that even between runs.
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 5 * time.Second, PermitWithoutStream: true}),
		// Run-end phase histograms for scenarios with many steps can
		// exceed the 4 MiB default.
		grpc.MaxRecvMsgSize(64 << 20),
	}
	if tlsCfg != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsCfg)))
	}
	return opts
}

// Capacity is what a worker advertised.
type Capacity struct {
	CPUs        int      `json:"cpus"`
	MemoryBytes uint64   `json:"memoryBytes"`
	MaxVUs      int      `json:"maxVUs"`
	Protocols   []string `json:"protocols"`
}

// WorkerInfo describes a registered worker.
type WorkerInfo struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Version        string            `json:"version"`
	Region         string            `json:"region,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Capacity       Capacity          `json:"capacity"`
	Connected      bool              `json:"connected"`
	ConnectedSince time.Time         `json:"connectedSince"`
	LastHeartbeat  time.Time         `json:"lastHeartbeat"`
	Health         wire.Health       `json:"health"`
	// ClockOffset is the worker's clock minus the server's, from the
	// latest synchronisation.
	ClockOffset time.Duration `json:"clockOffset"`
	CurrentRun  string        `json:"currentRun,omitempty"`
}

// Workers lists registered workers, sorted by id. A worker that dropped
// its connection during a run stays listed, disconnected, until the run
// ends.
func (c *Coordinator) Workers() []WorkerInfo {
	c.mu.Lock()
	ws := make([]*workerConn, 0, len(c.workers))
	for _, w := range c.workers {
		ws = append(ws, w)
	}
	c.mu.Unlock()
	out := make([]WorkerInfo, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// workerConn is the server's record of one worker. It survives
// reconnects: a worker that comes back with its id gets the same record,
// and so stays attached to its run.
type workerConn struct {
	id string

	lastSeen atomic.Int64 // time.Now Unix nanoseconds of the last message

	mu             sync.Mutex
	name, version  string
	region         string
	labels         map[string]string
	capacity       Capacity
	connectedSince time.Time
	stream         *serverStream
	lastHeartbeat  time.Time
	health         wire.Health
	offset, rtt    time.Duration
	run            *Run
	member         int
	pongs          map[int64]chan clockSample
}

// serverStream is one live Connect stream. A single writer goroutine owns
// Send, as gRPC requires.
type serverStream struct {
	out    chan *workerv1.ServerMessage
	ctx    context.Context
	cancel context.CancelFunc
}

func (w *workerConn) info() WorkerInfo {
	w.mu.Lock()
	defer w.mu.Unlock()
	wi := WorkerInfo{
		ID: w.id, Name: w.name, Version: w.version, Region: w.region,
		Labels: w.labels, Capacity: w.capacity, Connected: w.stream != nil,
		ConnectedSince: w.connectedSince, LastHeartbeat: w.lastHeartbeat,
		Health: w.health, ClockOffset: w.offset,
	}
	if w.run != nil {
		wi.CurrentRun = w.run.spec.ID
	}
	return wi
}

// send queues a message for the worker; it reports false when the worker
// is not connected or its queue is full.
func (w *workerConn) send(m *workerv1.ServerMessage) bool {
	w.mu.Lock()
	s := w.stream
	w.mu.Unlock()
	if s == nil {
		return false
	}
	select {
	case s.out <- m:
		return true
	case <-s.ctx.Done():
		return false
	default:
		return false
	}
}

func (w *workerConn) seen() time.Time { return time.Unix(0, w.lastSeen.Load()) }

// Connect implements WorkerService.
func (c *Coordinator) Connect(stream workerv1.WorkerService_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "the first message must be Hello")
	}
	if c.cfg.JoinToken == "" {
		return status.Error(codes.Unauthenticated, "this server has no join token configured, so it accepts no workers")
	}
	certName, err := c.checkWorkerCert(stream.Context())
	if err != nil {
		c.log.Warn("worker refused: no valid certificate", "name", hello.GetName(), "error", err)
		return err
	}
	if c.cfg.CA != nil {
		// The certificate is the credential; its name wins.
		hello.Name = certName
	} else if subtle.ConstantTimeCompare([]byte(hello.GetJoinToken()), []byte(c.cfg.JoinToken)) != 1 {
		c.log.Warn("worker refused: invalid join token", "name", hello.GetName())
		return status.Error(codes.Unauthenticated, "invalid join token")
	}
	if err := wire.CheckVersion(hello.GetProtocol()); err != nil {
		c.log.Warn("worker refused: incompatible protocol", "name", hello.GetName(), "error", err)
		return status.Error(codes.FailedPrecondition, err.Error())
	}

	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	ss := &serverStream{out: make(chan *workerv1.ServerMessage, 1024), ctx: ctx, cancel: cancel}
	w, resumed := c.attach(hello, ss)
	defer c.detach(w, ss)

	if err := stream.Send(&workerv1.ServerMessage{Msg: &workerv1.ServerMessage_Welcome{Welcome: &workerv1.Welcome{
		WorkerId: w.id, HeartbeatIntervalNs: int64(c.cfg.HeartbeatInterval), Protocol: wire.Version(),
	}}}); err != nil {
		return err
	}
	c.log.Info("worker connected", "worker", w.id, "name", hello.GetName(), "region", hello.GetRegion(),
		"cpus", hello.GetCapacity().GetCpus(), "resumed", resumed)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-ss.out:
				if err := stream.Send(m); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { <-writerDone }()
	defer cancel()

	// A worker that was mid-run when it lost its previous connection
	// either rejoins that run or is told to stop.
	if id := hello.GetActiveRunId(); id != "" {
		w.mu.Lock()
		r := w.run
		w.mu.Unlock()
		if r == nil || r.spec.ID != id {
			w.send(stopMsg(id, true, "the server no longer tracks this run"))
		} else {
			r.reattached(w)
		}
	}
	// Measure the clock offset now so Workers() can show it; runs measure
	// again just before they start.
	go func() {
		if off, rtt, err := w.syncClock(ctx, c.cfg.ClockSamples); err == nil {
			w.mu.Lock()
			w.offset, w.rtt = off, rtt
			w.mu.Unlock()
		}
	}()

	// Receive on a separate goroutine so a newer connection from the same
	// worker can end this handler even while Recv is blocked on a
	// half-dead transport.
	recvErr := make(chan error, 1)
	go func() {
		for {
			m, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			now := time.Now()
			w.lastSeen.Store(now.UnixNano())
			c.handle(w, m, now)
		}
	}()
	select {
	case err := <-recvErr:
		if ctx.Err() != nil || errors.Is(err, io.EOF) {
			return nil
		}
		return err
	case <-ctx.Done():
		return status.Error(codes.Aborted, "replaced by a newer connection from the same worker")
	}
}

// attach registers a worker or reattaches a returning one.
func (c *Coordinator) attach(h *workerv1.Hello, ss *serverStream) (*workerConn, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	w, resumed := c.workers[h.GetResumeWorkerId()]
	if resumed {
		w.mu.Lock()
		// Only the same worker may take its record back.
		if w.name != h.GetName() {
			resumed = false
		}
		w.mu.Unlock()
	}
	if !resumed {
		id := h.GetResumeWorkerId()
		if _, taken := c.workers[id]; id == "" || taken {
			id = newWorkerID()
		}
		w = &workerConn{id: id, pongs: map[int64]chan clockSample{}}
		c.workers[id] = w
	}
	cp := h.GetCapacity()
	w.mu.Lock()
	if old := w.stream; old != nil {
		old.cancel()
	}
	w.name, w.version, w.region = h.GetName(), h.GetVersion(), h.GetRegion()
	if w.region == "" {
		w.region = h.GetLabels()["region"]
	}
	w.labels = h.GetLabels()
	w.capacity = Capacity{
		CPUs: int(cp.GetCpus()), MemoryBytes: cp.GetMemoryBytes(),
		MaxVUs: int(cp.GetMaxVus()), Protocols: cp.GetProtocols(),
	}
	w.stream = ss
	w.connectedSince = time.Now()
	w.mu.Unlock()
	w.lastSeen.Store(time.Now().UnixNano())
	return w, resumed
}

// detach forgets a closed stream. Records of workers in a run are kept so
// the run can account for them; idle ones are removed.
func (c *Coordinator) detach(w *workerConn, ss *serverStream) {
	c.mu.Lock()
	defer c.mu.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stream != ss {
		return // replaced by a newer connection
	}
	w.stream = nil
	if w.run == nil {
		delete(c.workers, w.id)
	}
	c.log.Info("worker disconnected", "worker", w.id, "name", w.name)
}

// handle processes one message from a worker.
func (c *Coordinator) handle(w *workerConn, m *workerv1.WorkerMessage, now time.Time) {
	switch msg := m.GetMsg().(type) {
	case *workerv1.WorkerMessage_Heartbeat:
		hb := msg.Heartbeat
		w.mu.Lock()
		w.lastHeartbeat = now
		if h := wire.HealthFromProto(hb.GetHealth()); h != nil {
			w.health = *h
		}
		r := w.run
		w.mu.Unlock()
		w.send(&workerv1.ServerMessage{Msg: &workerv1.ServerMessage_Heartbeat{Heartbeat: &workerv1.ServerHeartbeat{
			ServerTimeUnixNano: now.UnixNano(),
		}}})
		// A worker running something this server does not know about
		// (for example after a server restart) must stop.
		if id := hb.GetActiveRunId(); id != "" && (r == nil || r.spec.ID != id) {
			w.send(stopMsg(id, true, "the server no longer tracks this run"))
		}
	case *workerv1.WorkerMessage_ClockPong:
		p := msg.ClockPong
		w.mu.Lock()
		ch := w.pongs[p.GetT1UnixNano()]
		delete(w.pongs, p.GetT1UnixNano())
		w.mu.Unlock()
		if ch != nil {
			ch <- clockSample{t1: p.GetT1UnixNano(), t2: p.GetT2UnixNano(), t3: p.GetT3UnixNano(), t4: now.UnixNano()}
		}
	case *workerv1.WorkerMessage_RunAccepted, *workerv1.WorkerMessage_Snapshot,
		*workerv1.WorkerMessage_RunFinished, *workerv1.WorkerMessage_RunFailed:
		w.mu.Lock()
		r, member := w.run, w.member
		w.mu.Unlock()
		if r == nil || r.spec.ID != runIDOf(m) {
			return
		}
		r.deliver(inMsg{member: member, msg: m, at: now})
	case *workerv1.WorkerMessage_Hello:
		c.log.Warn("ignoring repeated Hello", "worker", w.id)
	}
}

// syncClock measures the worker's clock offset with n round trips.
func (w *workerConn) syncClock(ctx context.Context, n int) (offset, rtt time.Duration, err error) {
	samples := make([]clockSample, 0, n)
	for i := 0; i < n; i++ {
		ch := make(chan clockSample, 1)
		t1 := time.Now().UnixNano()
		w.mu.Lock()
		w.pongs[t1] = ch
		w.mu.Unlock()
		if !w.send(&workerv1.ServerMessage{Msg: &workerv1.ServerMessage_ClockPing{ClockPing: &workerv1.ClockPing{T1UnixNano: t1}}}) {
			w.dropPong(t1)
			return 0, 0, errNotConnected(w.id)
		}
		t := time.NewTimer(2 * time.Second)
		select {
		case s := <-ch:
			samples = append(samples, s)
		case <-t.C:
			w.dropPong(t1)
		case <-ctx.Done():
			t.Stop()
			w.dropPong(t1)
			return 0, 0, ctx.Err()
		}
		t.Stop()
	}
	off, rtt, ok := estimateOffset(samples)
	if !ok {
		return 0, 0, errClockSync(w.id)
	}
	return off, rtt, nil
}

func (w *workerConn) dropPong(t1 int64) {
	w.mu.Lock()
	delete(w.pongs, t1)
	w.mu.Unlock()
}

func runIDOf(m *workerv1.WorkerMessage) string {
	switch msg := m.GetMsg().(type) {
	case *workerv1.WorkerMessage_RunAccepted:
		return msg.RunAccepted.GetRunId()
	case *workerv1.WorkerMessage_Snapshot:
		return msg.Snapshot.GetRunId()
	case *workerv1.WorkerMessage_RunFinished:
		return msg.RunFinished.GetRunId()
	case *workerv1.WorkerMessage_RunFailed:
		return msg.RunFailed.GetRunId()
	}
	return ""
}

func stopMsg(runID string, kill bool, reason string) *workerv1.ServerMessage {
	return &workerv1.ServerMessage{Msg: &workerv1.ServerMessage_StopRun{StopRun: &workerv1.StopRun{
		RunId: runID, Kill: kill, Reason: reason,
	}}}
}

func errNotConnected(id string) error { return fmt.Errorf("worker %s is not connected", id) }

func errClockSync(id string) error {
	return fmt.Errorf("worker %s did not answer clock synchronisation pings", id)
}

func newWorkerID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "w-" + hex.EncodeToString(b)
}
