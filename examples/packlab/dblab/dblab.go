// Package dblab is DBLab, the data tier of a shop: PostgreSQL tables for
// customers, products and orders, seeded into a database you give it, and
// an in-process Redis (miniredis) with a product cache and a popularity
// ranking. The databases pack runs its queries and commands straight
// against both; DBLab's own HTTP side only reports health and counts. It
// is the reference app for the databases pack.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - index: orders has no index on customer_id, so a customer's order
//     history scans the whole table (fix "index" adds one on
//     (customer_id, created_at)).
//   - hotrow: a trigger adds every new order to one running-totals row,
//     so concurrent checkouts queue on that row's lock until each commits
//     (fix "hotrow" spreads the totals over 16 rows by order id).
package dblab

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

// Schema is where DBLab's tables live; it is dropped and recreated on
// every start.
const Schema = "dblab"

// Sizes are the seeded row counts.
type Sizes struct{ Customers, Products, Orders int }

// Seeded returns the row counts DBLab seeds (smaller with -fast).
func Seeded(fast bool) Sizes {
	if fast {
		return Sizes{Customers: 2000, Products: 1000, Orders: 20000}
	}
	return Sizes{Customers: 20000, Products: 1000, Orders: 200000}
}

var regions = []string{"north", "south", "east", "west", "central"}

type server struct {
	cfg    labkit.Config
	schema string
	pool   *pgxpool.Pool
	redis  *miniredis.Miniredis
}

// Open seeds the PostgreSQL database in cfg.Postgres, starts Redis on
// cfg.Listen and returns the app.
func Open(cfg labkit.Config) (*labkit.App, error) {
	_, app, err := newServer(cfg, Schema)
	return app, err
}

func newServer(cfg labkit.Config, schema string) (*server, *labkit.App, error) {
	if cfg.Postgres == "" {
		return nil, nil, fmt.Errorf("DBLab needs a PostgreSQL database (-postgres or PACKLAB_POSTGRES_DSN)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.Postgres)
	if err != nil {
		return nil, nil, err
	}
	s := &server{cfg: cfg, schema: schema, pool: pool}
	if err := s.seed(ctx); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("seeding PostgreSQL: %w", err)
	}
	s.redis = miniredis.NewMiniRedis()
	addr := cfg.Listen
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	if err := s.redis.StartAddr(addr); err != nil {
		pool.Close()
		return nil, nil, err
	}
	s.seedRedis()
	dsn, err := withSearchPath(cfg.Postgres, schema)
	if err != nil {
		s.redis.Close()
		pool.Close()
		return nil, nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "DBLab", "schema": schema, "redis": s.redis.Addr(),
			"links": map[string]string{"stats": "/api/stats"}})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			labkit.Error(w, 503, "postgres_down", err.Error())
			return
		}
		labkit.JSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/stats", s.stats)
	return s, &labkit.App{
		Handler: mux,
		Env:     map[string]string{"SQL_DSN": dsn, "REDIS_ADDR": s.redis.Addr()},
		Close: func() {
			s.redis.Close()
			pool.Close()
		},
	}, nil
}

// withSearchPath adds search_path to a PostgreSQL connection string, so
// the pack's queries name tables without the schema.
func withSearchPath(dsn, schema string) (string, error) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", err
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	return dsn + " search_path=" + schema, nil
}

// seed recreates the schema: tables, the totals trigger, the data and,
// with the index fix, the index.
func (s *server) seed(ctx context.Context) error {
	n := Seeded(s.cfg.Fast)
	slot := "0"
	if s.cfg.Fixes.On("hotrow") {
		slot = "NEW.id % 16"
	}
	sch := pgx.Identifier{s.schema}.Sanitize()
	ddl := []string{
		`DROP SCHEMA IF EXISTS ` + sch + ` CASCADE`,
		`CREATE SCHEMA ` + sch,
		`SET search_path = ` + sch,
		`CREATE TABLE customers (id int PRIMARY KEY, name text NOT NULL, email text NOT NULL, region text NOT NULL)`,
		`CREATE TABLE products (id int PRIMARY KEY, name text NOT NULL, price numeric(10,2) NOT NULL, stock int NOT NULL)`,
		`CREATE TABLE orders (id bigserial PRIMARY KEY, customer_id int NOT NULL, product_id int NOT NULL,
			qty int NOT NULL, total numeric(10,2) NOT NULL, created_at timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE order_totals (slot int PRIMARY KEY, orders bigint NOT NULL, revenue numeric NOT NULL)`,
		`CREATE FUNCTION add_to_totals() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
		BEGIN
			INSERT INTO order_totals AS t (slot, orders, revenue) VALUES (` + slot + `, 1, NEW.total)
			ON CONFLICT (slot) DO UPDATE SET orders = t.orders + 1, revenue = t.revenue + NEW.total;
			RETURN NEW;
		END $$`,
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	// The session's search_path is reset when the connection goes back.
	defer conn.Exec(context.Background(), `RESET search_path`) //nolint:errcheck // best effort
	for _, q := range ddl {
		if _, err := conn.Exec(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", strings.Fields(q)[0], err)
		}
	}
	rng := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // seeded on purpose: every start has the same data
	var rows [][]any
	for i := 1; i <= n.Customers; i++ {
		rows = append(rows, []any{i, fmt.Sprintf("Customer %d", i), fmt.Sprintf("customer%d@dblab.test", i), regions[i%len(regions)]})
	}
	if _, err := conn.CopyFrom(ctx, pgx.Identifier{"customers"}, []string{"id", "name", "email", "region"}, pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	rows = rows[:0]
	for i := 1; i <= n.Products; i++ {
		rows = append(rows, []any{i, fmt.Sprintf("Product %d", i), price(rng, 1), 1_000_000})
	}
	if _, err := conn.CopyFrom(ctx, pgx.Identifier{"products"}, []string{"id", "name", "price", "stock"}, pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	// Order history over the last 30 days, loaded before the trigger.
	now := time.Now()
	rows = rows[:0]
	for range n.Orders {
		qty := 1 + rng.IntN(3)
		rows = append(rows, []any{1 + rng.IntN(n.Customers), 1 + rng.IntN(n.Products), qty,
			price(rng, qty), now.Add(-time.Duration(rng.Int64N(int64(30 * 24 * time.Hour))))})
	}
	if _, err := conn.CopyFrom(ctx, pgx.Identifier{"orders"}, []string{"customer_id", "product_id", "qty", "total", "created_at"}, pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	after := []string{
		`INSERT INTO order_totals (slot, orders, revenue) SELECT 0, count(*), coalesce(sum(total), 0) FROM orders`,
		`CREATE TRIGGER orders_totals AFTER INSERT ON orders FOR EACH ROW EXECUTE FUNCTION add_to_totals()`,
	}
	if s.cfg.Fixes.On("index") {
		after = append(after, `CREATE INDEX orders_customer ON orders (customer_id, created_at DESC)`)
	}
	after = append(after, `ANALYZE`)
	for _, q := range after {
		if _, err := conn.Exec(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", strings.Fields(q)[0], err)
		}
	}
	return nil
}

// price is qty items at 1.00 to 200.99 each.
func price(rng *rand.Rand, qty int) float64 {
	return float64(qty) * (float64(1+rng.IntN(200)) + float64(rng.IntN(100))/100)
}

// seedRedis fills the product cache (a hash per product) and the
// popularity ranking.
func (s *server) seedRedis() {
	rng := rand.New(rand.NewPCG(5, 6)) //nolint:gosec // seeded on purpose
	for i := 1; i <= Seeded(s.cfg.Fast).Products; i++ {
		id := strconv.Itoa(i)
		s.redis.HSet("product:"+id, "name", "Product "+id, "price", fmt.Sprintf("%d.%02d", 1+rng.IntN(200), rng.IntN(100)), "sold", "0")
		_, _ = s.redis.ZAdd("popular", float64(rng.IntN(1000)), id)
	}
}

func (s *server) stats(w http.ResponseWriter, r *http.Request) {
	var orders, slots int64
	var revenue string
	err := s.pool.QueryRow(r.Context(), `SELECT (SELECT count(*) FROM `+pgx.Identifier{s.schema, "orders"}.Sanitize()+`),
		count(*), coalesce(sum(revenue), 0)::text FROM `+pgx.Identifier{s.schema, "order_totals"}.Sanitize()).Scan(&orders, &slots, &revenue)
	if err != nil {
		labkit.Error(w, 502, "postgres_error", err.Error())
		return
	}
	labkit.JSON(w, 200, map[string]any{"orders": orders, "totalsRows": slots, "revenue": revenue, "redisKeys": len(s.redis.Keys())})
}
