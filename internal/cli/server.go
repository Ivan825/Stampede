package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/keyring"
	"github.com/Ivan825/Stampede/internal/safety"
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
	st, err := store.Open(ctx, f.databaseURL)
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
	srv, err := server.New(server.Config{
		TrustedProxies: proxies,
		Store:          st, Keyring: kr, Logger: log, UI: UI, SecureCookies: f.secureCookies,
		HardCaps: safety.Caps{MaxRate: f.maxRate, MaxVUs: f.maxVUs, MaxDuration: f.maxDuration},
	})
	if err != nil {
		return err
	}
	if err := srv.Recover(ctx); err != nil {
		return err
	}
	return srv.ListenAndServe(ctx, f.addr)
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
