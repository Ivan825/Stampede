// Package db opens the Postgres pool, applies embedded migrations and toggles
// the planted orders index.
package db

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool sizes for the SHOPLAB_FIX_POOL bottleneck.
const (
	SmallPoolMaxConns = 5  // planted: far too small for a busy API
	FixedPoolMaxConns = 40 // fixed: comfortably below Postgres' default max_connections=100
)

// OrdersIndexName is the index whose absence is the SHOPLAB_FIX_INDEX bottleneck.
const OrdersIndexName = "orders_user_created_idx"

// migrationLockID is an arbitrary constant for pg_advisory_lock so that several
// replicas (or serve + seed) starting together apply migrations exactly once.
const migrationLockID = 7_342_001

// Open creates a pgx pool. maxConns <= 0 keeps pgx's default. tracer may be nil.
func Open(ctx context.Context, url string, maxConns int32, tracer pgx.QueryTracer) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse DATABASE_URL: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
		cfg.MinConns = 0
	}
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	if tracer != nil {
		cfg.ConnConfig.Tracer = tracer
	}
	// Return timestamptz values in UTC regardless of the host's time zone so
	// API responses are stable.
	cfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		conn.TypeMap().RegisterType(&pgtype.Type{
			Name: "timestamptz", OID: pgtype.TimestamptzOID,
			Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
		})
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: open pool: %w", err)
	}
	return pool, nil
}

// WaitReady pings the database until it answers or ctx expires. Useful when
// the app container starts before Postgres finished booting.
func WaitReady(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	var last error
	for attempt := 1; ; attempt++ {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		last = pool.Ping(pctx)
		cancel()
		if last == nil {
			return nil
		}
		if attempt == 1 || attempt%10 == 0 {
			log.Info("waiting for postgres", "attempt", attempt, "err", last)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("db: postgres not ready: %w", last)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// Migration is one embedded SQL file.
type Migration struct {
	Version string // file name without extension, e.g. "0001_init"
	SQL     string
}

// LoadMigrations reads *.sql files from dir in fsys, sorted by name.
func LoadMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("db: read migrations: %w", err)
	}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("db: read %s: %w", e.Name(), err)
		}
		out = append(out, Migration{Version: strings.TrimSuffix(e.Name(), ".sql"), SQL: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Migrate applies every migration not yet recorded in schema_migrations, each
// in its own transaction, under a session advisory lock. It returns the
// versions it applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool, migrations []Migration) ([]string, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: acquire for migrate: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return nil, fmt.Errorf("db: migration lock: %w", err)
	}
	defer func() {
		// Use a fresh context so the unlock still happens if ctx was cancelled.
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(uctx, "SELECT pg_advisory_unlock($1)", migrationLockID)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return nil, fmt.Errorf("db: create schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("db: list migrations: %w", err)
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("db: list migrations: %w", err)
	}
	for _, v := range versions {
		applied[v] = true
	}

	var done []string
	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.SQL); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", m.Version)
			return err
		})
		if err != nil {
			return done, fmt.Errorf("db: apply migration %s: %w", m.Version, err)
		}
		done = append(done, m.Version)
	}
	return done, nil
}

// ApplyIndexFix creates the orders(user_id, created_at) index when fixed is
// true and drops it otherwise, so flipping SHOPLAB_FIX_INDEX takes effect on
// the same database at the next start.
func ApplyIndexFix(ctx context.Context, pool *pgxpool.Pool, fixed bool) error {
	var sql string
	if fixed {
		sql = "CREATE INDEX IF NOT EXISTS " + OrdersIndexName + " ON orders (user_id, created_at DESC)"
	} else {
		sql = "DROP INDEX IF EXISTS " + OrdersIndexName
	}
	if _, err := pool.Exec(ctx, sql); err != nil {
		return fmt.Errorf("db: toggle %s: %w", OrdersIndexName, err)
	}
	// Refresh planner statistics so the choice between index and seq scan is
	// made on real numbers right away.
	if _, err := pool.Exec(ctx, "ANALYZE orders"); err != nil {
		return fmt.Errorf("db: analyze orders: %w", err)
	}
	return nil
}

// IndexExists reports whether the named index exists in the current schema.
func IndexExists(ctx context.Context, pool *pgxpool.Pool, name string) (bool, error) {
	var ok bool
	err := pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1)",
		name).Scan(&ok)
	return ok, err
}
