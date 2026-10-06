package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/worker"
)

type workerFlags struct {
	server   string
	token    string
	name     string
	region   string
	labels   []string
	maxVUs   int
	insecure bool
	caFile   string
	verbose  bool
	mtls     bool
	caPin    string
}

func newWorkerCmd() *cobra.Command {
	f := &workerFlags{}
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Run a load-generating worker for a Stampede server",
		Long: `Run a worker that connects out to a Stampede server and generates its share
of every distributed run the server assigns. Only an outbound connection is
needed, so workers run fine behind NAT and firewalls.

The worker reconnects on its own if the connection drops. If it loses the
server for 10 seconds during a run it stops generating load by itself.

With --mtls (for servers started with --worker-mtls) the worker enrolls for
its own certificate from the server's built-in CA, proving it knows the join
token without sending it, and connects with that certificate. Pin the CA
with --ca-fingerprint, as logged by the server.

The join token can also be given in STAMPEDE_JOIN_TOKEN, which keeps it out
of the process list.`,
		Example: `  stampede worker --server stampede.internal:8081 --mtls --ca-fingerprint sha256:9f2c... --region mumbai
  stampede worker --server stampede.internal:7443 --token $TOKEN --region mumbai
  stampede worker --server 10.0.0.5:7443 --insecure --label pool=spot --max-vus 2000`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := f.config()
			if err != nil {
				return err
			}
			level := slog.LevelInfo
			if f.verbose {
				level = slog.LevelDebug
			}
			cfg.Logger = slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: level}))
			if f.insecure {
				cfg.Logger.Warn("connecting without TLS: the join token and scenario secrets travel in clear text")
			}
			if host, ok := strings.CutPrefix(cfg.Server, "dns:"); ok {
				return runDiscovered(cmd.Context(), cfg, host)
			}
			servers := splitServers(cfg.Server)
			if len(servers) == 1 {
				return worker.New(cfg).Run(cmd.Context())
			}
			// Several active server replicas: one connection to each, and
			// load for only one of them at a time.
			gate := &worker.Gate{}
			errc := make(chan error, len(servers))
			for _, addr := range servers {
				c := cfg
				c.Server, c.Gate = addr, gate
				c.Logger = cfg.Logger.With("server", addr)
				go func() { errc <- worker.New(c).Run(cmd.Context()) }()
			}
			var first error
			for range servers {
				if err := <-errc; err != nil && first == nil {
					first = err
				}
			}
			return first
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.server, "server", "", "server address, host:port (required); with several active replicas, all of them comma-separated, or dns:host:port to connect to every address the name resolves to")
	fl.StringVar(&f.token, "token", "", "join token (default $STAMPEDE_JOIN_TOKEN)")
	fl.StringVar(&f.name, "name", "", "worker name (default the host name)")
	fl.StringVar(&f.region, "region", "", "region label used to split load by region")
	fl.StringArrayVar(&f.labels, "label", nil, "extra label KEY=VALUE (repeatable)")
	fl.IntVar(&f.maxVUs, "max-vus", 0, "most virtual users this worker accepts (0 = no limit)")
	fl.BoolVar(&f.insecure, "insecure", false, "connect without TLS (trusted networks only)")
	fl.StringVar(&f.caFile, "ca", "", "PEM file of the CA that signed the server certificate (default the system roots)")
	fl.BoolVar(&f.mtls, "mtls", os.Getenv("STAMPEDE_WORKER_MTLS") == "true", "enroll with the server's built-in CA and connect with this worker's own certificate (server started with --worker-mtls)")
	fl.StringVar(&f.caPin, "ca-fingerprint", os.Getenv("STAMPEDE_CA_FINGERPRINT"), "with --mtls, the server CA's fingerprint (sha256:...); without it the CA is trusted on first enrollment, authenticated by the join token")
	fl.BoolVarP(&f.verbose, "verbose", "v", false, "debug logging")
	return cmd
}

func splitServers(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (f *workerFlags) config() (worker.Config, error) {
	cfg := worker.Config{
		Server: f.server, Token: f.token, Name: f.name, Region: f.region,
		MaxVUs: f.maxVUs, Insecure: f.insecure, MTLS: f.mtls, CAFingerprint: strings.TrimSpace(f.caPin),
	}
	if f.mtls && (f.insecure || f.caFile != "") {
		return cfg, errors.New("--mtls replaces --insecure and --ca; use one")
	}
	if f.caPin != "" && !f.mtls {
		return cfg, errors.New("--ca-fingerprint needs --mtls")
	}
	if p := cfg.CAFingerprint; p != "" && (!strings.HasPrefix(p, "sha256:") || len(p) != len("sha256:")+64) {
		return cfg, errors.New("--ca-fingerprint must look like sha256:<64 hex digits>, as printed by the server")
	}
	if cfg.Server == "" {
		return cfg, errors.New("--server is required")
	}
	if cfg.Token == "" {
		cfg.Token = os.Getenv("STAMPEDE_JOIN_TOKEN")
	}
	if cfg.Token == "" {
		return cfg, errors.New("a join token is required: pass --token or set STAMPEDE_JOIN_TOKEN")
	}
	if f.maxVUs < 0 {
		return cfg, errors.New("--max-vus must not be negative")
	}
	if len(f.labels) > 0 {
		cfg.Labels = map[string]string{}
	}
	for _, kv := range f.labels {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return cfg, fmt.Errorf("--label %q: use KEY=VALUE", kv)
		}
		cfg.Labels[k] = v
	}
	if f.caFile != "" {
		if f.insecure {
			return cfg, errors.New("--ca and --insecure contradict each other")
		}
		pem, err := os.ReadFile(f.caFile)
		if err != nil {
			return cfg, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return cfg, fmt.Errorf("%s holds no PEM certificates", f.caFile)
		}
		cfg.TLS = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return cfg, nil
}

// runDiscovered connects to every address host resolves to (a headless
// Kubernetes service lists every server replica), re-resolving every 30
// seconds so new replicas are joined and departed ones dropped. The
// connections share one gate, so the process runs one run at a time.
func runDiscovered(ctx context.Context, cfg worker.Config, hostport string) error {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return fmt.Errorf("--server dns:%s: use dns:host:port", hostport)
	}
	gate := &worker.Gate{}
	conns := map[string]context.CancelFunc{}
	defer func() {
		for _, cancel := range conns {
			cancel()
		}
	}()
	resolve := func() {
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		ips, err := net.DefaultResolver.LookupHost(rctx, host)
		if err != nil {
			cfg.Logger.Warn("could not resolve the servers", "name", host, "error", err)
			return
		}
		seen := map[string]bool{}
		for _, ip := range ips {
			addr := net.JoinHostPort(ip, port)
			seen[addr] = true
			if _, ok := conns[addr]; ok {
				continue
			}
			wctx, cancel := context.WithCancel(ctx)
			conns[addr] = cancel
			c := cfg
			c.Server, c.Gate, c.Logger = addr, gate, cfg.Logger.With("server", addr)
			cfg.Logger.Info("connecting to a server replica", "addr", addr)
			go func() {
				if err := worker.New(c).Run(wctx); err != nil && wctx.Err() == nil {
					c.Logger.Error("connection to replica ended", "error", err)
				}
			}()
		}
		for addr, cancel := range conns {
			if !seen[addr] {
				cfg.Logger.Info("server replica gone", "addr", addr)
				cancel()
				delete(conns, addr)
			}
		}
	}
	resolve()
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			resolve()
		}
	}
}
