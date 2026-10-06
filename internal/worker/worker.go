// Package worker runs the load-generating side of a distributed test. A
// worker dials out to the server (so it works from behind NAT), registers
// with a join token, keeps a heartbeat going and runs whatever slice of a
// scenario the server assigns, streaming one snapshot per interval back.
//
// A worker protects the target on its own: if it loses the server for the
// dead man's switch timeout (10s) during a run, it stops generating load
// without waiting to be told.
package worker

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	workerv1 "github.com/Ivan825/Stampede/gen/stampede/worker/v1"
	"github.com/Ivan825/Stampede/internal/engine"
	"github.com/Ivan825/Stampede/internal/pluginhost"
	"github.com/Ivan825/Stampede/internal/protocol/httpx"
	"github.com/Ivan825/Stampede/internal/version"
	"github.com/Ivan825/Stampede/internal/wire"
)

// Config configures a worker.
type Config struct {
	// Server is the coordinator address, host:port.
	Server string
	// Token is the server's join token.
	Token  string
	Name   string // default: the host name
	Region string
	Labels map[string]string
	// MaxVUs caps the virtual users this worker accepts (0 = no cap).
	MaxVUs int
	// CPUs overrides the advertised capacity (default runtime.NumCPU).
	// The server splits load in proportion to it.
	CPUs int

	// Insecure connects without TLS. Otherwise TLS is used with TLS (nil
	// means the system roots).
	Insecure bool
	TLS      *tls.Config
	// MTLS enrolls with the server's built-in CA and connects with the
	// worker's own certificate (servers started with --worker-mtls). The
	// join token is then never sent.
	MTLS bool
	// CAFingerprint pins the server's CA ("sha256:..."). Without it the
	// CA is learned at the first enrollment, authenticated by the join
	// token, and pinned from then on.
	CAFingerprint string
	// EnrollDialer replaces the TCP dialer used to enroll (tests).
	EnrollDialer interface {
		DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	}
	// DialOptions are added to the gRPC dial options (tests dial bufconn).
	DialOptions []grpc.DialOption

	// DeadManTimeout stops load when the server has not been heard from
	// for this long during a run (default 10s).
	DeadManTimeout time.Duration
	// Thresholds for self-reported saturation (default DefaultThresholds).
	Thresholds *Thresholds
	// HTTP overrides engine HTTP options (tests, network emulation).
	HTTP httpx.Options
	// PluginDir is where plugins are looked for before PATH (default
	// $STAMPEDE_PLUGIN_DIR or the user config directory).
	PluginDir string
	// Clock is the worker's wall clock (default time.Now). Tests use it to
	// simulate workers whose clocks disagree with the server's.
	Clock  func() time.Time
	Logger *slog.Logger

	// OnRunStart, when set, is called with each run's start time T0
	// converted to this process's time.Now clock.
	OnRunStart func(runID string, t0 time.Time)
	// OnStop, when set, is called when the worker stops a run on the
	// server's request or by its dead man's switch.
	OnStop func(runID string, kill bool, reason string)
}

// Worker is a load-generating worker.
type Worker struct {
	cfg Config
	log *slog.Logger
	mon *monitor

	// lastContact is when anything was last heard from the server, in
	// time.Now Unix nanoseconds. The dead man's switch watches it.
	lastContact atomic.Int64
	hbInterval  atomic.Int64

	idMu  sync.Mutex
	ident *identity

	mu   sync.Mutex
	id   string
	sess *session
	run  *activeRun
}

// protocols lists what this worker can drive: the built-in protocols and
// "plugin:<name>" for every plugin installed when it connects. A run
// re-checks its plugins when it starts, so a plugin installed later is
// still found.
func (w *Worker) protocols() []string {
	out := []string{"http", "graphql", "sse", "ws", "grpc"}
	if _, err := engine.FindChrome(); err == nil {
		out = append(out, "browser")
	}
	list, err := pluginhost.List(w.cfg.PluginDir)
	if err != nil {
		return out
	}
	for _, p := range list {
		out = append(out, "plugin:"+p.Name)
	}
	return out
}

// New returns a worker; call Run to connect.
func New(cfg Config) *Worker {
	if cfg.Name == "" {
		cfg.Name, _ = os.Hostname()
	}
	if cfg.CPUs <= 0 {
		cfg.CPUs = runtime.NumCPU()
	}
	if cfg.DeadManTimeout <= 0 {
		cfg.DeadManTimeout = 10 * time.Second
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	th := DefaultThresholds
	if cfg.Thresholds != nil {
		th = *cfg.Thresholds
	}
	w := &Worker{cfg: cfg, log: cfg.Logger, mon: newMonitor(th)}
	w.hbInterval.Store(int64(time.Second))
	return w
}

// ID is the id the server assigned (empty before the first Welcome).
func (w *Worker) ID() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.id
}

// Run connects to the server and serves runs until ctx is done,
// reconnecting with exponential backoff. It returns an error only when the
// server rejects the worker outright (wrong token, incompatible protocol).
func (w *Worker) Run(ctx context.Context) error {
	monDone := make(chan struct{})
	defer close(monDone)
	go w.mon.loop(monDone)

	const minBackoff, maxBackoff = 250 * time.Millisecond, 15 * time.Second
	backoff := minBackoff
	for {
		start := time.Now()
		err := w.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if permanent(err) {
			return err
		}
		if time.Since(start) > 30*time.Second {
			backoff = minBackoff
		}
		w.log.Warn("disconnected from server; reconnecting", "server", w.cfg.Server, "error", err, "in", backoff)
		// Jitter keeps a fleet of workers from reconnecting in lockstep
		// after a server restart.
		d := backoff/2 + rand.N(backoff/2+1)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(d):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// permanent reports whether retrying cannot help.
func permanent(err error) bool {
	switch status.Code(err) {
	case codes.Unauthenticated, codes.PermissionDenied, codes.FailedPrecondition:
		return true
	}
	return false
}

func (w *Worker) dialOptions(ctx context.Context) ([]grpc.DialOption, error) {
	opts := []grpc.DialOption{
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time: 10 * time.Second, Timeout: 5 * time.Second, PermitWithoutStream: true,
		}),
	}
	switch {
	case w.cfg.MTLS:
		creds, err := w.mtlsCreds(ctx)
		if err != nil {
			return nil, err
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	case w.cfg.Insecure:
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	case w.cfg.TLS != nil:
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(w.cfg.TLS)))
	default:
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})))
	}
	return append(opts, w.cfg.DialOptions...), nil
}

// joinToken is sent in Hello only without mutual TLS; with it the
// certificate is the credential.
func (w *Worker) joinToken() string {
	if w.cfg.MTLS {
		return ""
	}
	return w.cfg.Token
}

// session is one connected stream.
type session struct {
	stream workerv1.WorkerService_ConnectClient
	out    chan *workerv1.WorkerMessage
	ctx    context.Context
	cancel context.CancelFunc
}

// send queues a message. It never blocks: a message that cannot be queued
// is dropped, and snapshots are resent from the outbox on reconnect.
func (s *session) send(m *workerv1.WorkerMessage) {
	select {
	case s.out <- m:
	case <-s.ctx.Done():
	default:
	}
}

// sendWait queues a message, waiting for room unless the session ends.
func (s *session) sendWait(m *workerv1.WorkerMessage) {
	select {
	case s.out <- m:
	case <-s.ctx.Done():
	}
}

func (s *session) writeLoop() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case m := <-s.out:
			if err := s.stream.Send(m); err != nil {
				s.cancel()
				return
			}
		}
	}
}

func (w *Worker) currentSession() *session {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sess
}

func (w *Worker) touch() { w.lastContact.Store(time.Now().UnixNano()) }

// session runs one connection to the server until it breaks.
func (w *Worker) session(ctx context.Context) error {
	opts, err := w.dialOptions(ctx)
	if err != nil {
		return err
	}
	conn, err := grpc.NewClient(w.cfg.Server, opts...)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := workerv1.NewWorkerServiceClient(conn).Connect(sctx)
	if err != nil {
		return err
	}

	w.mu.Lock()
	hello := &workerv1.Hello{
		Protocol: wire.Version(), JoinToken: w.joinToken(), Name: w.cfg.Name,
		Version: version.Version, Region: w.cfg.Region, Labels: w.cfg.Labels,
		Capacity: &workerv1.Capacity{
			Cpus: uint32(w.cfg.CPUs), MemoryBytes: totalMemory(),
			MaxVus: uint32(max(w.cfg.MaxVUs, 0)), Protocols: w.protocols(),
		},
		ResumeWorkerId: w.id,
	}
	if w.run != nil {
		hello.ActiveRunId = w.run.id
	}
	w.mu.Unlock()
	if err := stream.Send(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_Hello{Hello: hello}}); err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	welcome := first.GetWelcome()
	if welcome == nil {
		return errors.New("server did not answer Hello with Welcome")
	}
	w.touch()
	if d := time.Duration(welcome.GetHeartbeatIntervalNs()); d > 0 {
		w.hbInterval.Store(int64(d))
	}

	s := &session{stream: stream, out: make(chan *workerv1.WorkerMessage, 1024), ctx: sctx, cancel: cancel}
	go s.writeLoop()
	w.attach(s, welcome.GetWorkerId())
	defer w.detach(s)
	w.log.Info("connected to server", "server", w.cfg.Server, "worker", welcome.GetWorkerId())
	go w.heartbeatLoop(s)

	for {
		m, err := stream.Recv()
		if err != nil {
			return err
		}
		received := w.cfg.Clock()
		w.touch()
		switch msg := m.GetMsg().(type) {
		case *workerv1.ServerMessage_ClockPing:
			s.send(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_ClockPong{ClockPong: &workerv1.ClockPong{
				T1UnixNano: msg.ClockPing.GetT1UnixNano(),
				T2UnixNano: received.UnixNano(),
				T3UnixNano: w.cfg.Clock().UnixNano(),
			}}})
		case *workerv1.ServerMessage_StartRun:
			w.startRun(ctx, s, msg.StartRun)
		case *workerv1.ServerMessage_StopRun:
			w.stopRun(msg.StopRun)
		case *workerv1.ServerMessage_Ack:
			w.ack(msg.Ack)
		case *workerv1.ServerMessage_Heartbeat, *workerv1.ServerMessage_Welcome:
			// Liveness only.
		}
	}
}

// attach makes s the live session and replays whatever the server has not
// acknowledged, before any new snapshot can be sent on s.
func (w *Worker) attach(s *session, id string) {
	w.mu.Lock()
	w.id = id
	r := w.run
	w.mu.Unlock()
	if r == nil {
		w.mu.Lock()
		w.sess = s
		w.mu.Unlock()
		return
	}
	// Holding r.mu while publishing the session keeps resends ahead of
	// snapshots produced concurrently.
	r.mu.Lock()
	defer r.mu.Unlock()
	// The writer is already draining, so a long backlog (up to an hour of
	// snapshots after a long outage) is replayed in full rather than
	// overflowing the queue.
	for _, p := range r.outbox {
		s.sendWait(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_Snapshot{Snapshot: p}})
	}
	if r.finished != nil {
		s.sendWait(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_RunFinished{RunFinished: r.finished}})
	}
	w.mu.Lock()
	w.sess = s
	w.mu.Unlock()
}

func (w *Worker) detach(s *session) {
	s.cancel()
	w.mu.Lock()
	if w.sess == s {
		w.sess = nil
	}
	w.mu.Unlock()
}

func (w *Worker) heartbeatLoop(s *session) {
	t := time.NewTicker(time.Duration(w.hbInterval.Load()))
	defer t.Stop()
	beat := func() {
		h := w.mon.current()
		hb := &workerv1.Heartbeat{WorkerTimeUnixNano: w.cfg.Clock().UnixNano(), Health: wire.HealthToProto(&h)}
		w.mu.Lock()
		r := w.run
		w.mu.Unlock()
		if r != nil {
			r.mu.Lock()
			if r.finished == nil {
				hb.ActiveRunId = r.id
				hb.Vus = uint32(max(r.eng.ActiveVUs(), 0))
			}
			r.mu.Unlock()
		}
		s.send(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_Heartbeat{Heartbeat: hb}})
	}
	beat()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			beat()
		}
	}
}

func (w *Worker) sendNow(m *workerv1.WorkerMessage) {
	if s := w.currentSession(); s != nil {
		s.send(m)
	}
}

func runFailed(runID string, err error) *workerv1.WorkerMessage {
	return &workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_RunFailed{RunFailed: &workerv1.RunFailed{
		RunId: runID, Error: err.Error(),
	}}}
}
