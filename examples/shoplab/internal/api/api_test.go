package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/examples/shoplab/internal/cache"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/config"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/session"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/shop"
)

// ------------------------------------------------------------------ fakes

type fakeShop struct {
	mu       sync.Mutex
	products map[int64]*shop.ProductDetail
	users    map[string]shop.User
	orders   map[int64][]shop.Order // by user
	nextID   int64
	detailN  atomic.Int64
	listArgs shop.ListParams
	failList error
}

func newFakeShop() *fakeShop {
	f := &fakeShop{products: map[int64]*shop.ProductDetail{}, users: map[string]shop.User{}, orders: map[int64][]shop.Order{}}
	cat := shop.Category{ID: 1, Name: "Shoes", Slug: "shoes"}
	f.products[1] = &shop.ProductDetail{ID: 1, SKU: "SKU-1", Name: "Trail Shoe", PriceCents: 2500, Price: 25, Stock: 5, Category: cat}
	f.products[2] = &shop.ProductDetail{ID: 2, SKU: "SKU-2", Name: "Road Shoe", PriceCents: 1000, Price: 10, Stock: 100, Category: cat}
	f.users["user0001@shoplab.test"] = shop.User{ID: 1, Email: "user0001@shoplab.test", Name: "User 1", PasswordHash: "hash:shoplab-pass"}
	return f
}

func (f *fakeShop) ListProducts(_ context.Context, p shop.ListParams) (shop.ProductPage, error) {
	f.listArgs = p
	if f.failList != nil {
		return shop.ProductPage{}, f.failList
	}
	return shop.ProductPage{Page: p.Page, PerPage: p.PerPage, Total: 2, Products: []shop.ProductSummary{{ID: 1, Name: "Trail Shoe"}}}, nil
}

func (f *fakeShop) ProductDetail(_ context.Context, id int64) (shop.ProductDetail, error) {
	f.detailN.Add(1)
	time.Sleep(10 * time.Millisecond) // the expensive part
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.products[id]
	if !ok {
		return shop.ProductDetail{}, shop.ErrNotFound
	}
	return *p, nil
}

func (f *fakeShop) ProductBriefs(_ context.Context, ids []int64) (map[int64]shop.ProductBrief, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int64]shop.ProductBrief{}
	for _, id := range ids {
		if p, ok := f.products[id]; ok {
			out[id] = shop.ProductBrief{ID: id, Name: p.Name, PriceCents: p.PriceCents, Stock: p.Stock}
		}
	}
	return out, nil
}

func (f *fakeShop) UserByEmail(_ context.Context, email string) (shop.User, error) {
	u, ok := f.users[email]
	if !ok {
		return u, shop.ErrNotFound
	}
	return u, nil
}

func (f *fakeShop) UserByID(_ context.Context, id int64) (shop.User, error) {
	for _, u := range f.users {
		if u.ID == id {
			return u, nil
		}
	}
	return shop.User{}, shop.ErrNotFound
}

func (f *fakeShop) Checkout(_ context.Context, userID int64, lines []shop.CartLine) (shop.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(lines) == 0 {
		return shop.Order{}, shop.ErrEmptyCart
	}
	var sub int64
	n := 0
	for _, l := range lines {
		p := f.products[l.ProductID]
		if p.Stock < l.Qty {
			return shop.Order{}, &shop.OutOfStockError{ProductID: p.ID, Requested: l.Qty, Available: p.Stock}
		}
	}
	for _, l := range lines {
		p := f.products[l.ProductID]
		p.Stock -= l.Qty
		sub += p.PriceCents * int64(l.Qty)
		n += l.Qty
	}
	tax, ship, total := shop.Totals(sub)
	f.nextID++
	o := shop.Order{ID: f.nextID, Status: "placed", SubtotalCents: sub, TaxCents: tax, ShippingCents: ship,
		TotalCents: total, Total: shop.Dollars(total), ItemCount: n, CreatedAt: time.Now()}
	f.orders[userID] = append(f.orders[userID], o)
	return o, nil
}

func (f *fakeShop) ListOrders(_ context.Context, userID int64, page, perPage int) (shop.OrderPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	os := f.orders[userID]
	if os == nil {
		os = []shop.Order{}
	}
	return shop.OrderPage{Page: page, PerPage: perPage, Total: int64(len(os)), Orders: os}, nil
}

func (f *fakeShop) Order(_ context.Context, userID, orderID int64) (shop.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.orders[userID] {
		if o.ID == orderID {
			return o, nil
		}
	}
	return shop.Order{}, shop.ErrNotFound
}

func (f *fakeShop) Oversold(context.Context) ([]shop.OversoldProduct, error) {
	return []shop.OversoldProduct{{ID: 1, Name: "Trail Shoe", Stock: 4, UnitsReceived: 5, UnitsSold: 9, TrueStock: -4, OversoldUnits: 4, LostUpdates: 8}}, nil
}

func (f *fakeShop) ResetLowStock(context.Context) (int64, error) { return 10, nil }

type fakeCarts struct {
	mu sync.Mutex
	m  map[string]map[int64]int
}

func (c *fakeCarts) SetQty(_ context.Context, id string, pid int64, qty int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m[id] == nil {
		c.m[id] = map[int64]int{}
	}
	c.m[id][pid] = qty
	return nil
}

func (c *fakeCarts) Remove(_ context.Context, id string, pid int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m[id], pid)
	return nil
}

func (c *fakeCarts) Lines(_ context.Context, id string) ([]shop.CartLine, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []shop.CartLine
	for pid, q := range c.m[id] {
		out = append(out, shop.CartLine{ProductID: pid, Qty: q})
	}
	return out, nil
}

func (c *fakeCarts) Clear(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, id)
	return nil
}

type memKV struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (k *memKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[key]
	return v, ok, nil
}

func (k *memKV) Set(_ context.Context, key string, v []byte, _ time.Duration) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key] = v
	return nil
}

// ----------------------------------------------------------------- harness

type harness struct {
	t    *testing.T
	srv  *httptest.Server
	shop *fakeShop
	kv   *memKV
}

func newHarness(t *testing.T, mutate func(*Deps)) *harness {
	t.Helper()
	fs := newFakeShop()
	kv := &memKV{m: map[string][]byte{}}
	d := Deps{
		Catalog: fs, Users: fs, Orders: fs, Inventory: fs,
		Carts:    &fakeCarts{m: map[string]map[int64]int{}},
		Sessions: session.New(time.Hour, true),
		Cache:    cache.New(kv, cache.Options{TTL: time.Minute}),
		Version:  "test",
		OpenAPI:  []byte("openapi: 3.1.0\n"),
		CheckPassword: func(hash, pw string) error {
			if hash != "hash:"+pw {
				return errors.New("mismatch")
			}
			return nil
		},
		FlushCache: func(context.Context) (int, error) {
			kv.mu.Lock()
			defer kv.mu.Unlock()
			n := len(kv.m)
			kv.m = map[string][]byte{}
			return n, nil
		},
		Ready: map[string]func(context.Context) error{
			"postgres": func(context.Context) error { return nil },
			"redis":    func(context.Context) error { return nil },
		},
	}
	if mutate != nil {
		mutate(&d)
	}
	srv := httptest.NewServer(NewRouter(d))
	t.Cleanup(srv.Close)
	return &harness{t: t, srv: srv, shop: fs, kv: kv}
}

type resp struct {
	status int
	header http.Header
	body   []byte
}

func (r resp) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (r resp) errCode(t *testing.T) string {
	t.Helper()
	var e ErrorBody
	r.json(t, &e)
	if e.Error.Code == "" || e.Error.Message == "" {
		t.Fatalf("error body missing code/message: %s", r.body)
	}
	return e.Error.Code
}

func (h *harness) do(method, path, token string, body any) resp {
	h.t.Helper()
	var rd io.Reader
	if s, ok := body.(string); ok {
		rd = strings.NewReader(s)
	} else if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{status: res.StatusCode, header: res.Header, body: b}
}

func (h *harness) login() string {
	h.t.Helper()
	r := h.do("POST", "/api/login", "", map[string]string{"email": "USER0001@shoplab.test", "password": "shoplab-pass"})
	if r.status != 200 {
		h.t.Fatalf("login: %d %s", r.status, r.body)
	}
	var lr loginResponse
	r.json(h.t, &lr)
	if len(lr.Token) != 64 || lr.TokenType != "Bearer" {
		h.t.Fatalf("bad login response %s", r.body)
	}
	return lr.Token
}

// ------------------------------------------------------------------- tests

func TestHealthAndReady(t *testing.T) {
	h := newHarness(t, nil)
	if r := h.do("GET", "/healthz", "", nil); r.status != 200 {
		t.Fatalf("healthz %d", r.status)
	}
	if r := h.do("GET", "/readyz", "", nil); r.status != 200 {
		t.Fatalf("readyz %d %s", r.status, r.body)
	}

	h = newHarness(t, func(d *Deps) {
		d.Ready["redis"] = func(context.Context) error { return errors.New("connection refused") }
	})
	r := h.do("GET", "/readyz", "", nil)
	if r.status != 503 || !strings.Contains(string(r.body), "connection refused") {
		t.Fatalf("readyz with redis down: %d %s", r.status, r.body)
	}
}

func TestIndexShowsFixes(t *testing.T) {
	h := newHarness(t, func(d *Deps) { d.Fixes = config.Fixes{Race: true} })
	var body struct{ Fixes map[string]bool }
	h.do("GET", "/", "", nil).json(t, &body)
	if !body.Fixes["race"] || body.Fixes["n1"] || len(body.Fixes) != 6 {
		t.Fatalf("fixes = %v", body.Fixes)
	}
}

func TestUnknownRouteAndMethodAreJSON(t *testing.T) {
	h := newHarness(t, nil)
	r := h.do("GET", "/nope", "", nil)
	if r.status != 404 || r.errCode(t) != "not_found" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	r = h.do("PUT", "/api/products", "", nil)
	if r.status != 405 || r.errCode(t) != "method_not_allowed" {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestListProductsParams(t *testing.T) {
	h := newHarness(t, nil)
	r := h.do("GET", "/api/products?page=2&per_page=5&q=shoe&category=shoes", "", nil)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	want := shop.ListParams{Page: 2, PerPage: 5, Query: "shoe", Category: "shoes"}
	if h.shop.listArgs != want {
		t.Fatalf("list args = %+v", h.shop.listArgs)
	}
	for _, q := range []string{"page=0", "page=x", "per_page=101", "per_page=-1"} {
		r := h.do("GET", "/api/products?"+q, "", nil)
		if r.status != 400 || r.errCode(t) != "invalid_query" {
			t.Errorf("%s: %d %s", q, r.status, r.body)
		}
	}
}

func TestListProductsInternalErrorIsHidden(t *testing.T) {
	h := newHarness(t, func(d *Deps) {})
	h.shop.failList = errors.New("pq: secret details")
	r := h.do("GET", "/api/products", "", nil)
	if r.status != 500 || r.errCode(t) != "internal" || strings.Contains(string(r.body), "secret") {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestProductDetailCaching(t *testing.T) {
	h := newHarness(t, nil)
	r := h.do("GET", "/api/products/1", "", nil)
	if r.status != 200 || r.header.Get("X-Cache") != "miss" {
		t.Fatalf("first: %d %s %s", r.status, r.header.Get("X-Cache"), r.body)
	}
	var d shop.ProductDetail
	r.json(t, &d)
	if d.ID != 1 || d.Category.Slug != "shoes" {
		t.Fatalf("detail = %+v", d)
	}
	r = h.do("GET", "/api/products/1", "", nil)
	if r.header.Get("X-Cache") != "hit" {
		t.Fatalf("second X-Cache = %s", r.header.Get("X-Cache"))
	}
	if h.shop.detailN.Load() != 1 {
		t.Fatalf("detail loads = %d", h.shop.detailN.Load())
	}
	if r := h.do("GET", "/api/products/999", "", nil); r.status != 404 || r.errCode(t) != "not_found" {
		t.Fatalf("missing: %d %s", r.status, r.body)
	}
	if r := h.do("GET", "/api/products/abc", "", nil); r.status != 400 || r.errCode(t) != "invalid_id" {
		t.Fatalf("bad id: %d %s", r.status, r.body)
	}
}

func TestAdminFlushGatedAndWorks(t *testing.T) {
	h := newHarness(t, nil)
	if r := h.do("POST", "/admin/cache/flush", "", nil); r.status != 404 {
		t.Fatalf("flush without SHOPLAB_ADMIN: %d", r.status)
	}
	if r := h.do("POST", "/admin/stock/reset", "", nil); r.status != 404 {
		t.Fatalf("reset without SHOPLAB_ADMIN: %d", r.status)
	}

	h = newHarness(t, func(d *Deps) { d.Admin = true })
	h.do("GET", "/api/products/1", "", nil)
	r := h.do("POST", "/admin/cache/flush", "", nil)
	var out map[string]int
	r.json(t, &out)
	if r.status != 200 || out["flushed"] != 1 {
		t.Fatalf("flush: %d %s", r.status, r.body)
	}
	if r := h.do("GET", "/api/products/1", "", nil); r.header.Get("X-Cache") != "miss" {
		t.Fatal("expected miss after flush")
	}
	if r := h.do("POST", "/admin/stock/reset", "", nil); r.status != 200 {
		t.Fatalf("reset: %d", r.status)
	}
}

func TestOversoldEndpoint(t *testing.T) {
	h := newHarness(t, func(d *Deps) { d.OversoldDetected = func() int64 { return 7 } })
	r := h.do("GET", "/admin/oversold", "", nil)
	var out struct {
		OversoldUnits      int64
		DetectedSinceStart int64
		Products           []shop.OversoldProduct
	}
	r.json(t, &out)
	if r.status != 200 || out.OversoldUnits != 4 || out.DetectedSinceStart != 7 || len(out.Products) != 1 {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestLogin(t *testing.T) {
	h := newHarness(t, nil)
	h.login()
	cases := []struct {
		body any
		code int
		err  string
	}{
		{map[string]string{"email": "user0001@shoplab.test", "password": "wrong"}, 401, "invalid_credentials"},
		{map[string]string{"email": "ghost@shoplab.test", "password": "x"}, 401, "invalid_credentials"},
		{map[string]string{"email": "", "password": "x"}, 400, "validation_failed"},
		{`{"email":`, 400, "invalid_json"},
	}
	for _, c := range cases {
		r := h.do("POST", "/api/login", "", c.body)
		if r.status != c.code || r.errCode(t) != c.err {
			t.Errorf("%v: %d %s", c.body, r.status, r.body)
		}
	}
}

func TestAuthRequired(t *testing.T) {
	h := newHarness(t, nil)
	for _, p := range []struct{ m, path string }{
		{"GET", "/api/me"}, {"GET", "/api/cart"}, {"POST", "/api/cart"},
		{"DELETE", "/api/cart/1"}, {"POST", "/api/checkout"},
		{"GET", "/api/orders"}, {"GET", "/api/orders/1"},
	} {
		for _, tok := range []string{"", "deadbeef"} {
			r := h.do(p.m, p.path, tok, nil)
			if r.status != 401 || r.errCode(t) != "unauthorized" {
				t.Errorf("%s %s token=%q: %d %s", p.m, p.path, tok, r.status, r.body)
			}
		}
	}
	tok := h.login()
	r := h.do("GET", "/api/me", tok, nil)
	var u shop.User
	r.json(t, &u)
	if r.status != 200 || u.Email != "user0001@shoplab.test" || strings.Contains(string(r.body), "hash") {
		t.Fatalf("me: %d %s", r.status, r.body)
	}
}

func TestCartAndCheckoutFlow(t *testing.T) {
	h := newHarness(t, nil)
	tok := h.login()

	if r := h.do("POST", "/api/checkout", tok, nil); r.status != 400 || r.errCode(t) != "empty_cart" {
		t.Fatalf("empty checkout: %d %s", r.status, r.body)
	}
	for _, bad := range []any{
		map[string]any{"productId": 0, "qty": 1},
		map[string]any{"productId": 1, "qty": 0},
		map[string]any{"productId": 1, "qty": 100},
	} {
		if r := h.do("POST", "/api/cart", tok, bad); r.status != 400 || r.errCode(t) != "validation_failed" {
			t.Errorf("%v: %d %s", bad, r.status, r.body)
		}
	}
	if r := h.do("POST", "/api/cart", tok, map[string]any{"productId": 42, "qty": 1}); r.status != 404 {
		t.Fatalf("unknown product: %d", r.status)
	}

	h.do("POST", "/api/cart", tok, map[string]any{"productId": 2, "qty": 3})
	r := h.do("POST", "/api/cart", tok, map[string]any{"productId": 1, "qty": 2})
	var cart shop.CartView
	r.json(t, &cart)
	if r.status != 200 || len(cart.Items) != 2 || cart.ItemCount != 5 || cart.SubtotalCents != 8000 || cart.Items[0].ProductID != 1 {
		t.Fatalf("cart: %d %s", r.status, r.body)
	}
	r = h.do("DELETE", "/api/cart/2", tok, nil)
	r.json(t, &cart)
	if len(cart.Items) != 1 || cart.SubtotalCents != 5000 {
		t.Fatalf("after delete: %s", r.body)
	}

	r = h.do("POST", "/api/checkout", tok, nil)
	var co checkoutResponse
	r.json(t, &co)
	if r.status != 201 || co.OrderID != 1 || co.TotalCents != 5413 || co.Total != 54.13 {
		t.Fatalf("checkout: %d %s", r.status, r.body)
	}
	r = h.do("GET", "/api/cart", tok, nil)
	r.json(t, &cart)
	if len(cart.Items) != 0 {
		t.Fatalf("cart not cleared: %s", r.body)
	}

	// Product 1 now has 3 left; asking for 4 conflicts.
	h.do("POST", "/api/cart", tok, map[string]any{"productId": 1, "qty": 4})
	r = h.do("POST", "/api/checkout", tok, nil)
	var e ErrorBody
	r.json(t, &e)
	if r.status != 409 || e.Error.Code != "out_of_stock" {
		t.Fatalf("oos: %d %s", r.status, r.body)
	}
	det := e.Error.Details.(map[string]any)
	if det["available"].(float64) != 3 || det["requested"].(float64) != 4 {
		t.Fatalf("details = %v", det)
	}

	r = h.do("GET", "/api/orders", tok, nil)
	var page shop.OrderPage
	r.json(t, &page)
	if r.status != 200 || page.Total != 1 || page.Orders[0].ID != 1 {
		t.Fatalf("orders: %d %s", r.status, r.body)
	}
	if r := h.do("GET", "/api/orders/1", tok, nil); r.status != 200 {
		t.Fatalf("order 1: %d", r.status)
	}
	if r := h.do("GET", "/api/orders/2", tok, nil); r.status != 404 || r.errCode(t) != "not_found" {
		t.Fatalf("order 2: %d %s", r.status, r.body)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	h := newHarness(t, nil)
	h.do("GET", "/api/products/1", "", nil)
	h.do("GET", "/nope", "", nil)
	r := h.do("GET", "/metrics", "", nil)
	body := string(r.body)
	for _, want := range []string{
		`shoplab_http_request_duration_seconds_count{method="GET",route="/api/products/{id}",status="200"} 1`,
		`route="unmatched",status="404"`,
		`shoplab_fix_enabled{fix="cache"} 0`,
		"go_memstats_heap_inuse_bytes",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

func TestOpenAPIServed(t *testing.T) {
	h := newHarness(t, nil)
	r := h.do("GET", "/openapi.yaml", "", nil)
	if r.status != 200 || r.header.Get("Content-Type") != "application/yaml" || !strings.HasPrefix(string(r.body), "openapi: 3.1.0") {
		t.Fatalf("%d %s %q", r.status, r.header.Get("Content-Type"), r.body)
	}
}
