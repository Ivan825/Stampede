//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"
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
	"github.com/Ivan825/Stampede/examples/shoplab/internal/shop"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/store"
)

var (
	setupOnce sync.Once
	setupErr  error
)

func urls(t *testing.T) (string, string) {
	t.Helper()
	dbURL, redisURL := os.Getenv("DATABASE_URL"), os.Getenv("REDIS_URL")
	if dbURL == "" || redisURL == "" {
		t.Skip("DATABASE_URL and REDIS_URL must be set for integration tests")
	}
	return dbURL, redisURL
}

func openPool(t *testing.T, maxConns int32, tracer pgx.QueryTracer) *pgxpool.Pool {
	t.Helper()
	dbURL, _ := urls(t)
	pool, err := db.Open(context.Background(), dbURL, maxConns, tracer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	setupOnce.Do(func() { setupErr = setup(pool) })
	if setupErr != nil {
		t.Fatal(setupErr)
	}
	return pool
}

// setup migrates and, if the database is empty, seeds a small dataset.
func setup(pool *pgxpool.Pool) error {
	ctx := context.Background()
	ms, err := db.LoadMigrations(shoplab.Migrations, "migrations")
	if err != nil {
		return err
	}
	if _, err := db.Migrate(ctx, pool, ms); err != nil {
		return err
	}
	_, err = seed.Run(ctx, pool, seed.Options{
		Products: 2000, Users: 200, OrdersPerUser: 10, HotReviews: 1000, IfEmpty: true,
	}, slog.New(slog.DiscardHandler))
	return err
}

func openRedis(t *testing.T) *redis.Client {
	t.Helper()
	_, redisURL := urls(t)
	rdb, err := redisstore.Connect(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rdb.Close() })
	return rdb
}

func TestMigrationsAreIdempotent(t *testing.T) {
	pool := openPool(t, 4, nil)
	ms, _ := db.LoadMigrations(shoplab.Migrations, "migrations")
	applied, err := db.Migrate(context.Background(), pool, ms)
	if err != nil || len(applied) != 0 {
		t.Fatalf("second migrate applied %v, err %v", applied, err)
	}
}

// Bottleneck 1: N+1. Count statements per listing request.
func TestNPlusOneQueryCount(t *testing.T) {
	m := metrics.New(nil)
	pool := openPool(t, 10, m.QueryTracer())
	ctx := context.Background()
	queries := func() float64 { return testutil.ToFloat64(m.DBQueries.WithLabelValues("ok")) }

	results := map[bool]shop.ProductPage{}
	for _, fixed := range []bool{false, true} {
		st := store.New(pool, store.Options{FixN1: fixed})
		before := queries()
		page, err := st.ListProducts(ctx, shop.ListParams{Page: 1, PerPage: 20})
		if err != nil {
			t.Fatal(err)
		}
		n := queries() - before
		results[fixed] = page

		const iters = 50
		start := time.Now()
		for range iters {
			if _, err := st.ListProducts(ctx, shop.ListParams{Page: 2, PerPage: 20}); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("fix_n1=%v: %v statements per 20-item page, %v per request (sequential avg)", fixed, n, time.Since(start)/iters)
		want := 2.0 // count + page
		if !fixed {
			want = 2 + 2*20
		}
		if n != want {
			t.Errorf("fix_n1=%v: %v statements, want %v", fixed, n, want)
		}
	}
	a, b := results[false], results[true]
	if a.Total != b.Total || len(a.Products) != len(b.Products) {
		t.Fatalf("results differ: %d/%d vs %d/%d", a.Total, len(a.Products), b.Total, len(b.Products))
	}
	for i := range a.Products {
		if a.Products[i] != b.Products[i] {
			t.Fatalf("product %d differs:\n%+v\n%+v", i, a.Products[i], b.Products[i])
		}
	}
}

// Bottleneck 2: missing index. The planner must switch from a sequential
// scan to the index when the fix is applied.
func TestOrdersIndexToggle(t *testing.T) {
	pool := openPool(t, 4, nil)
	ctx := context.Background()
	had, err := db.IndexExists(ctx, pool, db.OrdersIndexName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.ApplyIndexFix(context.Background(), pool, had) })

	plan := func() string {
		rows, err := pool.Query(ctx, `EXPLAIN SELECT id FROM orders WHERE user_id = 7 ORDER BY created_at DESC LIMIT 20`)
		if err != nil {
			t.Fatal(err)
		}
		lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(lines, "\n")
	}
	for _, fixed := range []bool{false, true, false} {
		if err := db.ApplyIndexFix(ctx, pool, fixed); err != nil {
			t.Fatal(err)
		}
		exists, _ := db.IndexExists(ctx, pool, db.OrdersIndexName)
		p := plan()
		if exists != fixed {
			t.Fatalf("fix_index=%v but index exists=%v", fixed, exists)
		}
		if fixed && !strings.Contains(p, db.OrdersIndexName) {
			t.Errorf("fixed plan does not use the index:\n%s", p)
		}
		if !fixed && !strings.Contains(p, "Seq Scan on orders") {
			t.Errorf("unfixed plan should scan orders:\n%s", p)
		}
	}
}

// Bottleneck 3: pool size. Run the same burst of real queries through a 5-
// and a 40-connection pool and compare pool waits.
func TestPoolWaits(t *testing.T) {
	ctx := context.Background()
	for _, size := range []int32{db.SmallPoolMaxConns, db.FixedPoolMaxConns} {
		pool := openPool(t, size, nil)
		st := store.New(pool, store.Options{FixN1: true})
		// warm up connections so connection setup is not measured
		var wg sync.WaitGroup
		for range size {
			wg.Go(func() { _ = pool.Ping(ctx) })
		}
		wg.Wait()
		base := pool.Stat()
		start := time.Now()
		for i := range 40 {
			wg.Go(func() {
				if _, err := st.ProductDetail(ctx, int64(1+i%10)); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		s := pool.Stat()
		waits := s.EmptyAcquireCount() - base.EmptyAcquireCount()
		waited := s.EmptyAcquireWaitTime() - base.EmptyAcquireWaitTime()
		t.Logf("max_conns=%d: 40 concurrent product details in %v, %d acquisitions waited, %v total wait",
			size, time.Since(start).Round(time.Millisecond), waits, waited.Round(time.Millisecond))
		if size == db.SmallPoolMaxConns && waits < 20 {
			t.Errorf("5-conn pool: only %d waits for 40 concurrent requests", waits)
		}
		if size == db.FixedPoolMaxConns && waits > 5 {
			t.Errorf("40-conn pool: %d waits for 40 concurrent requests", waits)
		}
	}
}

// Bottleneck 4: cache stampede on a cold popular product.
func TestCacheStampede(t *testing.T) {
	pool := openPool(t, 40, nil)
	rdb := openRedis(t)
	ctx := context.Background()
	st := store.New(pool, store.Options{FixN1: true})
	kv := redisstore.NewKV(rdb)

	for _, fixed := range []bool{false, true} {
		c := cache.New(kv, cache.Options{TTL: 10 * time.Second, Fixed: fixed})
		if _, err := redisstore.FlushPrefix(ctx, rdb, cache.ProductKeyPrefix); err != nil {
			t.Fatal(err)
		}
		var loads atomic.Int64
		load := func(ctx context.Context) ([]byte, error) {
			loads.Add(1)
			d, err := st.ProductDetail(ctx, 1)
			if err != nil {
				return nil, err
			}
			return json.Marshal(d)
		}
		const clients = 100
		var wg sync.WaitGroup
		gate := make(chan struct{})
		for range clients {
			wg.Go(func() {
				<-gate
				if _, _, err := c.Fetch(ctx, cache.ProductKey(1), load); err != nil {
					t.Error(err)
				}
			})
		}
		start := time.Now()
		close(gate)
		wg.Wait()
		t.Logf("fix_cache=%v: %d concurrent cold requests -> %d expensive recomputations in %v",
			fixed, clients, loads.Load(), time.Since(start).Round(time.Millisecond))
		if !fixed && loads.Load() < clients/2 {
			t.Errorf("naive cache recomputed only %d times; stampede not reproduced", loads.Load())
		}
		if fixed && loads.Load() != 1 {
			t.Errorf("fixed cache recomputed %d times, want 1", loads.Load())
		}
	}
}

// Bottleneck 5: oversell. Many buyers race for a product with 5 units.
func TestOversellRace(t *testing.T) {
	pool := openPool(t, 40, nil)
	ctx := context.Background()
	const productID = 3
	const buyers = 40

	run := func(fixed bool) (ok, oos int, reported int64, report *shop.OversoldProduct) {
		var reportedUnits atomic.Int64
		st := store.New(pool, store.Options{FixRace: fixed, OnOversell: func(_ int64, u int64) { reportedUnits.Add(u) }})
		if _, err := st.ResetLowStock(ctx); err != nil {
			t.Fatal(err)
		}
		var (
			wg         sync.WaitGroup
			nOK, nOOS  atomic.Int64
			gate       = make(chan struct{})
			unexpected atomic.Value
		)
		for i := range buyers {
			wg.Go(func() {
				<-gate
				_, err := st.Checkout(ctx, int64(1+i), []shop.CartLine{{ProductID: productID, Qty: 1}})
				var o *shop.OutOfStockError
				switch {
				case err == nil:
					nOK.Add(1)
				case errors.As(err, &o):
					nOOS.Add(1)
				default:
					unexpected.Store(err)
				}
			})
		}
		close(gate)
		wg.Wait()
		if err, _ := unexpected.Load().(error); err != nil {
			t.Fatal(err)
		}
		list, err := st.Oversold(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i := range list {
			if list[i].ID == productID {
				report = &list[i]
			}
		}
		return int(nOK.Load()), int(nOOS.Load()), reportedUnits.Load(), report
	}
	t.Cleanup(func() { _, _ = store.New(pool, store.Options{}).ResetLowStock(context.Background()) })

	// Unfixed: the race is probabilistic, so allow a few rounds.
	var oversold bool
	for round := 1; round <= 3 && !oversold; round++ {
		ok, oos, reported, rep := run(false)
		t.Logf("fix_race=false round %d: %d checkouts succeeded for %d units (%d out of stock), %d units reported oversold, report=%+v",
			round, ok, shop.LowStockQty, oos, reported, rep)
		if ok > shop.LowStockQty {
			oversold = true
			if rep == nil || rep.OversoldUnits != int64(ok-shop.LowStockQty) || reported != rep.OversoldUnits {
				t.Errorf("oversell accounting mismatch: ok=%d reported=%d report=%+v", ok, reported, rep)
			}
		}
	}
	if !oversold {
		t.Error("unfixed checkout never oversold in 3 rounds")
	}

	ok, oos, reported, rep := run(true)
	t.Logf("fix_race=true: %d checkouts succeeded, %d out of stock, %d reported oversold", ok, oos, reported)
	if ok != shop.LowStockQty || oos != buyers-shop.LowStockQty || reported != 0 || rep != nil {
		t.Errorf("fixed checkout: ok=%d oos=%d reported=%d report=%+v", ok, oos, reported, rep)
	}
	var stock int
	if err := pool.QueryRow(ctx, "SELECT stock FROM products WHERE id = $1", productID).Scan(&stock); err != nil || stock != 0 {
		t.Errorf("stock after fixed run = %d (%v), want 0", stock, err)
	}
}

// End-to-end through HTTP with real Postgres and Redis.
func TestHTTPJourney(t *testing.T) {
	pool := openPool(t, 10, nil)
	rdb := openRedis(t)
	st := store.New(pool, store.Options{FixN1: true, FixRace: true})
	h := api.NewRouter(api.Deps{
		Catalog: st, Users: st, Orders: st, Inventory: st,
		Carts:    redisstore.NewCarts(rdb, time.Hour),
		Sessions: session.New(time.Hour, true),
		Cache:    cache.New(redisstore.NewKV(rdb), cache.Options{TTL: 10 * time.Second, Fixed: true}),
		Fixes:    config.Fixes{N1: true, Race: true, Cache: true},
		OpenAPI:  shoplab.OpenAPI,
		Ready: map[string]func(context.Context) error{
			"postgres": pool.Ping,
			"redis":    func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
		},
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	call := func(method, path, token string, body any, want int) []byte {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, srv.URL+path, rd)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, res.StatusCode, want, b)
		}
		return b
	}

	call("GET", "/readyz", "", nil, 200)
	call("GET", "/api/products?q=shoe&category=shoes", "", nil, 200)
	call("GET", "/api/products/50", "", nil, 200)

	var login struct{ Token string }
	json.Unmarshal(call("POST", "/api/login", "", map[string]string{"email": seed.UserEmail(5), "password": seed.Password}, 200), &login)
	call("POST", "/api/login", "", map[string]string{"email": seed.UserEmail(5), "password": "wrong"}, 401)

	var before shop.OrderPage
	json.Unmarshal(call("GET", "/api/orders", login.Token, nil, 200), &before)

	call("POST", "/api/checkout", login.Token, nil, 400)
	call("POST", "/api/cart", login.Token, map[string]any{"productId": 50, "qty": 2}, 200)
	call("POST", "/api/cart", login.Token, map[string]any{"productId": 51, "qty": 1}, 200)
	var co struct {
		OrderID    int64
		TotalCents int64
	}
	json.Unmarshal(call("POST", "/api/checkout", login.Token, nil, 201), &co)

	var o shop.Order
	json.Unmarshal(call("GET", fmt.Sprintf("/api/orders/%d", co.OrderID), login.Token, nil, 200), &o)
	if o.TotalCents != co.TotalCents || len(o.Items) != 2 || o.ItemCount != 3 {
		t.Fatalf("order = %+v", o)
	}
	var after shop.OrderPage
	json.Unmarshal(call("GET", "/api/orders", login.Token, nil, 200), &after)
	if after.Total != before.Total+1 || after.Orders[0].ID != co.OrderID {
		t.Fatalf("orders before=%d after=%d first=%d", before.Total, after.Total, after.Orders[0].ID)
	}
	var cart shop.CartView
	json.Unmarshal(call("GET", "/api/cart", login.Token, nil, 200), &cart)
	if len(cart.Items) != 0 {
		t.Fatal("cart not cleared after checkout")
	}
}
