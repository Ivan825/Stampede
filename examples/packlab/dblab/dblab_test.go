package dblab

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

var schemas atomic.Int64

// start seeds a schema of its own, so these tests can share a database
// with packs_test, which uses Schema.
func start(t *testing.T, fixes string) (*server, map[string]string) {
	t.Helper()
	dsn := os.Getenv("STAMPEDE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set STAMPEDE_TEST_POSTGRES_DSN to a PostgreSQL database")
	}
	schema := fmt.Sprintf("dblab_test_%d_%d", os.Getpid(), schemas.Add(1))
	s, app, err := newServer(labkit.Config{Fixes: labkit.ParseFixes(fixes), Fast: true, Listen: "127.0.0.1:0", Postgres: dsn}, schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		app.Close()
	})
	return s, app.Env
}

// connect opens a pool with the DSN the pack gets.
func connect(t *testing.T, env map[string]string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), env["SQL_DSN"])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestSeededAndReachable(t *testing.T) {
	s, env := start(t, "")
	db := connect(t, env)
	n := Seeded(true)
	var customers, orders int
	// Unqualified names: the DSN carries the search path.
	if err := db.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM customers), (SELECT count(*) FROM orders)`).Scan(&customers, &orders); err != nil {
		t.Fatal(err)
	}
	if customers != n.Customers || orders != n.Orders {
		t.Fatalf("%d customers, %d orders", customers, orders)
	}
	if s.redis.HGet("product:7", "name") != "Product 7" || env["REDIS_ADDR"] != s.redis.Addr() {
		t.Fatalf("redis not seeded: %v", s.redis.Keys()[:3])
	}
}

func TestIndexBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "index"} {
		_, env := start(t, fixes)
		var plan []string
		rows, err := connect(t, env).Query(context.Background(), `EXPLAIN SELECT id FROM orders WHERE customer_id = 42 ORDER BY created_at DESC LIMIT 20`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var line string
			_ = rows.Scan(&line)
			plan = append(plan, line)
		}
		rows.Close()
		scans := strings.Contains(strings.Join(plan, "\n"), "Seq Scan on orders")
		if fixes == "" && !scans {
			t.Errorf("without the fix a customer's orders scan the table:\n%s", strings.Join(plan, "\n"))
		}
		if fixes != "" && scans {
			t.Errorf("with the fix the index is used:\n%s", strings.Join(plan, "\n"))
		}
	}
}

func TestHotRowBottleneck(t *testing.T) {
	for _, tc := range []struct {
		fixes string
		rows  int
	}{{"", 1}, {"hotrow", 16}} {
		_, env := start(t, tc.fixes)
		db := connect(t, env)
		var wg sync.WaitGroup
		for i := range 64 {
			wg.Go(func() {
				if _, err := db.Exec(context.Background(), `INSERT INTO orders (customer_id, product_id, qty, total) VALUES ($1, 1, 1, 10)`, i+1); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		var rows, orders int
		if err := db.QueryRow(context.Background(), `SELECT count(*), sum(orders) FROM order_totals`).Scan(&rows, &orders); err != nil {
			t.Fatal(err)
		}
		// Slot 0 holds the seeded history too.
		if rows != tc.rows || orders != Seeded(true).Orders+64 {
			t.Errorf("fixes %q: totals over %d rows counting %d orders", tc.fixes, rows, orders)
		}
	}
}
