package cli

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"google.golang.org/grpc"

	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/keyring"
	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/server"
	"github.com/Ivan825/Stampede/internal/store"
)

// UI is the embedded web app, set by the main package when it is built in.
var UI fs.FS

type serverFlags struct {
	addr          string
	databaseURL   string
	migrateOnly   bool
	migrateDryRun bool
	secureCookies bool
	logLevel      string
	logFormat     string
	maxRate       float64
	maxVUs        int
	maxDuration   time.Duration
	trusted       []string
	abortErrors   string
	abortFor      time.Duration
	workerAddr    string
	joinToken     string
	executor      string
	dataDir       string
	tlsCert       string
	tlsKey        string
}

func newServerCmd() *cobra.Command {
	f := &serverFlags{}
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Run the control plane: REST API, run manager and web UI",
		Long: `Run the Stampede control plane. It needs PostgreSQL (TimescaleDB recommended)
and applies database migrations on start.

Environment:
  STAMPEDE_DATABASE_URL     postgres://user:pass@host:5432/stampede
  STAMPEDE_MASTER_KEY       32 random bytes, base64, for encrypting secrets
                            (generate one with: stampede keygen)
  STAMPEDE_MASTER_KEY_FILE  or read the key from a file`,
		RunE: func(cmd *cobra.Command, _ []string) error { return runServer(cmd, f) },
	}
	fl := cmd.Flags()
	fl.StringVar(&f.addr, "addr", envOr("STAMPEDE_ADDR", ":8080"), "listen address")
	fl.StringVar(&f.databaseURL, "database-url", os.Getenv("STAMPEDE_DATABASE_URL"), "PostgreSQL URL")
	fl.BoolVar(&f.migrateOnly, "migrate-only", false, "apply migrations and exit")
	fl.BoolVar(&f.migrateDryRun, "migrate-dry-run", false, "report pending migrations and exit")
	fl.BoolVar(&f.secureCookies, "secure-cookies", os.Getenv("STAMPEDE_SECURE_COOKIES") == "true", "mark session cookies Secure (use behind HTTPS)")
	fl.StringVar(&f.logLevel, "log-level", envOr("STAMPEDE_LOG_LEVEL", "info"), "debug, info, warn or error")
	fl.StringVar(&f.logFormat, "log-format", envOr("STAMPEDE_LOG_FORMAT", "json"), "json or text")
	fl.Float64Var(&f.maxRate, "max-rate", 0, "hard cap on arrival rate for every run (0 = none)")
	fl.IntVar(&f.maxVUs, "max-vus", 0, "hard cap on virtual users for every run (0 = none)")
	fl.DurationVar(&f.maxDuration, "max-duration", 0, "hard cap on run duration (0 = none)")
	fl.StringVar(&f.workerAddr, "worker-addr", envOr("STAMPEDE_WORKER_ADDR", ":8081"), "gRPC address workers connect to (empty disables workers)")
	fl.StringVar(&f.joinToken, "join-token", os.Getenv("STAMPEDE_JOIN_TOKEN"), "secret workers present to join (required for workers)")
	fl.StringVar(&f.executor, "executor", envOr("STAMPEDE_EXECUTOR", "auto"), "auto (workers when connected, else in-process), workers or local")
	fl.StringVar(&f.dataDir, "data-dir", os.Getenv("STAMPEDE_DATA_DIR"), "directory holding CSV/JSON feeder files for runs")
	fl.StringVar(&f.tlsCert, "worker-tls-cert", os.Getenv("STAMPEDE_WORKER_TLS_CERT"), "TLS certificate for the worker port")
	fl.StringVar(&f.tlsKey, "worker-tls-key", os.Getenv("STAMPEDE_WORKER_TLS_KEY"), "TLS key for the worker port")
	fl.StringVar(&f.abortErrors, "abort-errors", envOr("STAMPEDE_ABORT_ERRORS", "90%"), "stop any run whose error rate stays at or above this (0 disables)")
	fl.DurationVar(&f.abortFor, "abort-for", 30*time.Second, "how long --abort-errors must hold before a run is stopped")
	fl.StringSliceVar(&f.trusted, "trusted-proxy", splitEnv("STAMPEDE_TRUSTED_PROXIES"), "CIDR of a reverse proxy whose X-Forwarded-For is trusted (repeatable)")
	return cmd
}

func splitEnv(k string) []string {
	v := os.Getenv(k)
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func newLogger(level, format string) *slog.Logger {
	var lv slog.Level
	_ = lv.UnmarshalText([]byte(level))
	opts := &slog.HandlerOptions{Level: lv}
	if format == "text" {
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, opts))
}

func runServer(cmd *cobra.Command, f *serverFlags) error {
	ctx := cmd.Context()
	log := newLogger(f.logLevel, f.logFormat)
	slog.SetDefault(log)
	if f.databaseURL == "" {
		return errors.New("set --database-url or STAMPEDE_DATABASE_URL")
	}
	st, err := openStoreWithRetry(ctx, log, f.databaseURL, 60*time.Second)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx, log, f.migrateDryRun); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if f.migrateOnly || f.migrateDryRun {
		return nil
	}

	kr, err := keyring.FromEnv()
	if errors.Is(err, keyring.ErrNoKey) {
		log.Warn("no master key configured; storing secrets is disabled", "hint", "set STAMPEDE_MASTER_KEY (stampede keygen)")
		kr = nil
	} else if err != nil {
		return err
	}

	var proxies []netip.Prefix
	for _, c := range f.trusted {
		pfx, err := netip.ParsePrefix(strings.TrimSpace(c))
		if err != nil {
			return fmt.Errorf("--trusted-proxy %q: %w", c, err)
		}
		proxies = append(proxies, pfx)
	}
	cfg := server.Config{DataDir: f.dataDir}
	local := &server.LocalExecutor{Logger: log}
	switch f.executor {
	case "local":
		cfg.Executor = local
	case "auto", "workers":
		if f.workerAddr == "" || f.joinToken == "" {
			if f.executor == "workers" {
				return errors.New("--executor workers needs --worker-addr and --join-token")
			}
			log.Info("workers disabled (set STAMPEDE_JOIN_TOKEN to enable); running load in-process")
			cfg.Executor = local
			break
		}
		coord := coordinator.New(coordinator.Config{JoinToken: f.joinToken, Logger: log})
		var tlsCfg *tls.Config
		if f.tlsCert != "" {
			cert, err := tls.LoadX509KeyPair(f.tlsCert, f.tlsKey)
			if err != nil {
				return fmt.Errorf("worker TLS: %w", err)
			}
			tlsCfg = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		}
		gs := grpc.NewServer(coordinator.ServerOptions(tlsCfg)...)
		coord.Register(gs)
		ln, err := net.Listen("tcp", f.workerAddr)
		if err != nil {
			return fmt.Errorf("worker port: %w", err)
		}
		go func() {
			if err := gs.Serve(ln); err != nil {
				log.Error("worker gRPC server stopped", "error", err)
			}
		}()
		defer gs.GracefulStop()
		log.Info("accepting workers", "addr", ln.Addr().String(), "tls", tlsCfg != nil)
		dist := &server.DistributedExecutor{Coordinator: coord}
		cfg.Workers = server.CoordinatorWorkers(coord)
		if f.executor == "workers" {
			cfg.Executor = dist
		} else {
			cfg.Executor = &server.AutoExecutor{Local: local, Distributed: dist}
		}
	default:
		return fmt.Errorf("--executor must be auto, workers or local")
	}
	cfg.Store, cfg.Keyring, cfg.Logger, cfg.UI, cfg.SecureCookies = st, kr, log, UI, f.secureCookies
	cfg.HardCaps = safety.Caps{MaxRate: f.maxRate, MaxVUs: f.maxVUs, MaxDuration: f.maxDuration}
	cfg.TrustedProxies = proxies
	if f.abortErrors != "" && f.abortErrors != "0" && f.abortErrors != "0%" {
		p, err := scenario.ParsePercent(f.abortErrors)
		if err != nil {
			return fmt.Errorf("--abort-errors: %w", err)
		}
		cfg.AbortFloor = &scenario.Abort{Errors: &p, For: scenario.Duration(f.abortFor)}
	}
	srv, err := server.New(cfg)
	if err != nil {
		return err
	}
	// Only one replica runs load at a time. Others wait as standbys and take
	// over when the leader's database session ends, so a crashed leader's
	// runs are settled exactly once.
	lock, err := becomeLeader(ctx, log, st, f.addr)
	if err != nil {
		return err
	}
	defer lock.Release()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	go watchLeadership(ctx, cancel, log, lock)
	if err := srv.Recover(ctx); err != nil {
		return err
	}
	if err := srv.ListenAndServe(ctx, f.addr); err != nil {
		return err
	}
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return nil
}

func newKeygenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "keygen",
		Short: "Print a new random master key for STAMPEDE_MASTER_KEY",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), keyring.GenerateKey())
		},
	}
}

// openStoreWithRetry waits for the database, which may still be starting
// (TimescaleDB restarts once while initialising a new data directory).
func openStoreWithRetry(ctx context.Context, log *slog.Logger, url string, wait time.Duration) (*store.Store, error) {
	deadline := time.Now().Add(wait)
	delay := 250 * time.Millisecond
	for {
		st, err := store.Open(ctx, url)
		if err == nil {
			return st, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, err
		}
		log.Warn("database not ready, retrying", "error", err, "in", delay)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		delay = min(delay*2, 5*time.Second)
	}
}

// becomeLeader returns once this replica holds the leader lock. While it
// waits it serves /healthz (200) and /readyz (503), so orchestrators keep it
// alive but route no traffic to it.
func becomeLeader(ctx context.Context, log *slog.Logger, st *store.Store, addr string) (*store.Lock, error) {
	lock, err := st.AdvisoryLock(ctx, store.LeaderLockKey)
	if err != nil || lock != nil {
		return lock, err
	}
	log.Info("another replica is active; waiting as a standby")
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("standby\n")) })
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "standby replica: another Stampede server is active", http.StatusServiceUnavailable)
	})
	hs := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = hs.ListenAndServe() }()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
		lock, err := st.AdvisoryLock(ctx, store.LeaderLockKey)
		if err != nil {
			log.Warn("leader lock check failed", "error", err)
			continue
		}
		if lock != nil {
			log.Info("became the active replica")
			return lock, nil
		}
	}
}

// watchLeadership stops the server if the session holding the leader lock
// dies: another replica may already have taken over, and two leaders would
// both drive the same runs.
func watchLeadership(ctx context.Context, cancel context.CancelCauseFunc, log *slog.Logger, lock *store.Lock) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	fails := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		pctx, pcancel := context.WithTimeout(ctx, 2*time.Second)
		err := lock.Alive(pctx)
		pcancel()
		if err == nil {
			fails = 0
			continue
		}
		fails++
		log.Warn("leader lock connection check failed", "error", err, "consecutive", fails)
		if fails >= 3 {
			log.Error("lost the leader lock; shutting down so a standby can take over")
			cancel(errors.New("lost the leader lock"))
			return
		}
	}
}
