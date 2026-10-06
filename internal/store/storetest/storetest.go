// Package storetest starts a disposable TimescaleDB for tests.
package storetest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Ivan825/Stampede/internal/store"
)

// Image is the database image used in tests, matching the Compose stack.
const Image = "timescale/timescaledb:2.22.1-pg17"

var (
	once    sync.Once
	baseURL string
	initErr error
)

// URL returns a connection URL for a fresh, migrated database. Set
// STAMPEDE_TEST_DATABASE_URL to use an existing server instead of Docker.
// Tests are skipped when neither is available.
func URL(t testing.TB) string {
	t.Helper()
	once.Do(func() {
		if u := os.Getenv("STAMPEDE_TEST_DATABASE_URL"); u != "" {
			baseURL = u
			return
		}
		ctx := context.Background()
		c, err := postgres.Run(ctx, Image,
			postgres.WithDatabase("stampede"), postgres.WithUsername("stampede"), postgres.WithPassword("stampede"),
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90*time.Second)),
		)
		if err != nil {
			initErr = err
			return
		}
		baseURL, initErr = c.ConnectionString(ctx, "sslmode=disable")
	})
	if initErr != nil {
		t.Skipf("no test database (Docker unavailable?): %v", initErr)
	}
	// Each caller gets its own database so tests can run in parallel.
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	name := "t_" + uuid.NewString()[:8]
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgx.ParseConfig(baseURL)
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", cfg.User, cfg.Password, cfg.Host, cfg.Port, name)
}

// Open returns a migrated store on a fresh database, closed at test end.
func Open(t testing.TB) *store.Store {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), false); err != nil {
		t.Fatal(err)
	}
	return s
}
