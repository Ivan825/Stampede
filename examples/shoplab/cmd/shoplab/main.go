// Command shoplab is the sample e-commerce API that ships with Stampede.
//
//	shoplab [serve]      run the HTTP API (default)
//	shoplab seed         load deterministic demo data
//	shoplab migrate      apply migrations and exit
//	shoplab users-csv    print the email,password data-feeder CSV
//	shoplab healthcheck  GET /healthz (for distroless container healthchecks)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/Ivan825/Stampede/examples/shoplab"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/api"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/cache"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/config"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/db"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/metrics"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/redisstore"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/seed"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/session"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/store"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `ShopLab - sample e-commerce API with planted performance bottlenecks.

Usage:
  shoplab [serve]                 run the HTTP API (default)
  shoplab seed [flags]            load deterministic demo data
  shoplab migrate                 apply migrations and exit
  shoplab users-csv [--users N]   print the email,password CSV for N users
  shoplab healthcheck [--url U]   exit 0 if /healthz answers 200
  shoplab version                 print the version

Configuration is read from the environment; see README.md.
`

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "seed":
		err = runSeed(args)
	case "migrate":
		err = runMigrate(args)
	case "users-csv":
		err = usersCSV(args, os.Stdout)
	case "healthcheck":
		err = healthcheck(args)
	case "version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoplab:", err)
		os.Exit(1)
	}
}

func newLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})).With("service", "shoplab")
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	m := metrics.New(cfg.Fixes.Map())

	maxConns := int32(db.SmallPoolMaxConns)
	if cfg.Fixes.Pool {
		maxConns = db.FixedPoolMaxConns
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL, maxConns, m.QueryTracer())
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := waitFor(ctx, 60*time.Second, func(c context.Context) error { return db.WaitReady(c, pool, log) }); err != nil {
		return err
	}
	if err := migrate(ctx, pool, log); err != nil {
		return err
	}
	if err := db.ApplyIndexFix(ctx, pool, cfg.Fixes.Index); err != nil {
		return err
	}
	log.Info("orders index", "name", db.OrdersIndexName, "present", cfg.Fixes.Index)

	rdb, err := redisstore.Connect(cfg.RedisURL)
	if err != nil {
		return err
	}
	defer rdb.Close()
	if err := waitFor(ctx, 60*time.Second, func(c context.Context) error { return waitRedis(c, rdb, log) }); err != nil {
		return err
	}

	sessions := session.New(cfg.SessionTTL, cfg.Fixes.Leak)
	go sessions.RunJanitor(ctx, min(cfg.SessionTTL/2, 30*time.Second))
	m.RegisterSessionStore(sessions.Len, sessions.Bytes)
	m.RegisterPool(pool)

	var oversold atomic.Int64
	st := store.New(pool, store.Options{
		FixN1:   cfg.Fixes.N1,
		FixRace: cfg.Fixes.Race,
		OnOversell: func(productID, units int64) {
			oversold.Add(units)
			m.Oversold.Add(float64(units))
			log.Warn("oversell detected", "product_id", productID, "units", units)
		},
	})
	productCache := cache.New(redisstore.NewKV(rdb), cache.Options{
		TTL: cfg.CacheTTL, Fixed: cfg.Fixes.Cache, Observer: cacheObserver{m}, Logger: log,
	})

	handler := api.NewRouter(api.Deps{
		Catalog: st, Users: st, Orders: st, Inventory: st,
		Carts:    redisstore.NewCarts(rdb, cfg.SessionTTL),
		Sessions: sessions,
		Cache:    productCache,
		Metrics:  m,
		Logger:   log,
		Fixes:    cfg.Fixes,
		Admin:    cfg.Admin,
		Version:  version,
		OpenAPI:  shoplab.OpenAPI,
		FlushCache: func(ctx context.Context) (int, error) {
			return redisstore.FlushPrefix(ctx, rdb, cache.ProductKeyPrefix)
		},
		Ready: map[string]func(context.Context) error{
			"postgres": pool.Ping,
			"redis":    func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
		},
		OversoldDetected: oversold.Load,
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("shoplab listening", "addr", cfg.Addr, "version", version, "fixes", cfg.Fixes.Map(),
		"pool_max_conns", maxConns, "cache_ttl", cfg.CacheTTL.String(), "session_ttl", cfg.SessionTTL.String(), "admin", cfg.Admin)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			return err
		}
	}
	return nil
}

type cacheObserver struct{ m *metrics.Metrics }

func (o cacheObserver) Request(s cache.Status) { o.m.CacheRequests.WithLabelValues(string(s)).Inc() }
func (o cacheObserver) Load(d time.Duration) {
	o.m.CacheLoads.Inc()
	o.m.CacheLoadDur.Observe(d.Seconds())
}

func waitFor(ctx context.Context, d time.Duration, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return fn(ctx)
}

func waitRedis(ctx context.Context, rdb *redis.Client, log *slog.Logger) error {
	for attempt := 1; ; attempt++ {
		err := rdb.Ping(ctx).Err()
		if err == nil {
			return nil
		}
		if attempt == 1 || attempt%10 == 0 {
			log.Info("waiting for redis", "attempt", attempt, "err", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("redis not ready: %w", err)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	ms, err := db.LoadMigrations(shoplab.Migrations, "migrations")
	if err != nil {
		return err
	}
	applied, err := db.Migrate(ctx, pool, ms)
	if err != nil {
		return err
	}
	if len(applied) > 0 {
		log.Info("migrations applied", "versions", applied)
	}
	return nil
}

// openForTool opens a small pool for the seed/migrate subcommands and runs
// migrations.
func openForTool(ctx context.Context, log *slog.Logger) (*pgxpool.Pool, error) {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return nil, err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL, 4, nil)
	if err != nil {
		return nil, err
	}
	if err := waitFor(ctx, 60*time.Second, func(c context.Context) error { return db.WaitReady(c, pool, log) }); err != nil {
		pool.Close()
		return nil, err
	}
	if err := migrate(ctx, pool, log); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func runMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := newLogger(slog.LevelInfo)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := openForTool(ctx, log)
	if err != nil {
		return err
	}
	pool.Close()
	log.Info("migrations up to date")
	return nil
}

func runSeed(args []string) error {
	d := seed.Defaults()
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	fs.IntVar(&d.Products, "products", d.Products, "number of products")
	fs.IntVar(&d.Users, "users", d.Users, "number of users (user0001@shoplab.test ...)")
	fs.IntVar(&d.OrdersPerUser, "orders-per-user", d.OrdersPerUser, "historic orders per user (raise to make the missing index hurt)")
	fs.IntVar(&d.HotReviews, "hot-reviews", d.HotReviews, "reviews on each popular product (first 1% of ids, min 10)")
	fs.BoolVar(&d.Reset, "reset", false, "truncate existing data before seeding")
	fs.BoolVar(&d.IfEmpty, "if-empty", false, "do nothing if the database already has products")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := newLogger(slog.LevelInfo)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := openForTool(ctx, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	log.Info("seeding", "products", d.Products, "users", d.Users, "orders_per_user", d.OrdersPerUser,
		"hot_reviews", d.HotReviews, "reset", d.Reset)
	st, err := seed.Run(ctx, pool, d, log)
	if err != nil {
		return err
	}
	if st.Skipped {
		return nil
	}
	log.Info("seed complete", "categories", st.Categories, "products", st.Products, "users", st.Users,
		"orders", st.Orders, "order_items", st.OrderItems, "reviews", st.Reviews, "duration", st.Duration.Round(time.Millisecond).String())
	return nil
}

func usersCSV(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("users-csv", flag.ExitOnError)
	n := fs.Int("users", 1000, "number of users")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return seed.WriteUsersCSV(w, *n)
}

func healthcheck(args []string) error {
	addr := os.Getenv("SHOPLAB_ADDR")
	if addr == "" {
		addr = config.DefaultAddr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	fs := flag.NewFlagSet("healthcheck", flag.ExitOnError)
	url := fs.String("url", "http://"+net.JoinHostPort(host, port)+"/healthz", "URL to probe")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Get(*url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", *url, res.Status)
	}
	return nil
}
