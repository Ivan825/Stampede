package cli

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

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

The join token can also be given in STAMPEDE_JOIN_TOKEN, which keeps it out
of the process list.`,
		Example: `  stampede worker --server stampede.internal:7443 --token $TOKEN --region mumbai
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
			return worker.New(cfg).Run(cmd.Context())
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.server, "server", "", "server address, host:port (required)")
	fl.StringVar(&f.token, "token", "", "join token (default $STAMPEDE_JOIN_TOKEN)")
	fl.StringVar(&f.name, "name", "", "worker name (default the host name)")
	fl.StringVar(&f.region, "region", "", "region label used to split load by region")
	fl.StringArrayVar(&f.labels, "label", nil, "extra label KEY=VALUE (repeatable)")
	fl.IntVar(&f.maxVUs, "max-vus", 0, "most virtual users this worker accepts (0 = no limit)")
	fl.BoolVar(&f.insecure, "insecure", false, "connect without TLS (trusted networks only)")
	fl.StringVar(&f.caFile, "ca", "", "PEM file of the CA that signed the server certificate (default the system roots)")
	fl.BoolVarP(&f.verbose, "verbose", "v", false, "debug logging")
	return cmd
}

func (f *workerFlags) config() (worker.Config, error) {
	cfg := worker.Config{
		Server: f.server, Token: f.token, Name: f.name, Region: f.region,
		MaxVUs: f.maxVUs, Insecure: f.insecure,
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
