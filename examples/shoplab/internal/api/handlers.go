package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Ivan825/Stampede/examples/shoplab/internal/cache"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/shop"
)

// MaxCartQty caps the quantity of a single product in a cart.
const MaxCartQty = 99

// ------------------------------------------------------------ operational

func (s *server) index(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":    "ShopLab",
		"version": s.Version,
		"fixes":   s.Fixes.Map(),
		"admin":   s.Admin,
		"links": map[string]string{
			"openapi": "/openapi.yaml",
			"metrics": "/metrics",
			"health":  "/healthz",
			"ready":   "/readyz",
		},
	})
}

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	checks := map[string]string{}
	ready := true
	for name, check := range s.Ready {
		if err := check(ctx); err != nil {
			checks[name] = err.Error()
			ready = false
		} else {
			checks[name] = "ok"
		}
	}
	status, code := "ready", http.StatusOK
	if !ready {
		status, code = "unavailable", http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"status": status, "checks": checks})
}

func (s *server) openapi(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(s.OpenAPI)
}

// --------------------------------------------------------------- products

func (s *server) listProducts(w http.ResponseWriter, r *http.Request) {
	page, ok := queryInt(w, r, "page", 1)
	if !ok {
		return
	}
	perPage, ok := queryInt(w, r, "per_page", 20)
	if !ok {
		return
	}
	if perPage > 100 {
		writeError(w, http.StatusBadRequest, "invalid_query", "per_page must be between 1 and 100")
		return
	}
	q := r.URL.Query()
	res, err := s.Catalog.ListProducts(r.Context(), shop.ListParams{
		Page: page, PerPage: perPage, Query: q.Get("q"), Category: q.Get("category"),
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) getProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	load := func(ctx context.Context) ([]byte, error) {
		d, err := s.Catalog.ProductDetail(ctx, id)
		if err != nil {
			return nil, err
		}
		return json.Marshal(d)
	}
	var (
		body   []byte
		status = cache.Miss
		err    error
	)
	if s.Cache != nil {
		body, status, err = s.Cache.Fetch(r.Context(), cache.ProductKey(id), load)
	} else {
		body, err = load(r.Context())
	}
	switch {
	case errors.Is(err, shop.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "product not found")
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", string(status))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ------------------------------------------------------------------- auth

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string    `json:"token"`
	TokenType string    `json:"tokenType"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "email and password are required")
		return
	}
	u, err := s.Users.UserByEmail(r.Context(), req.Email)
	if errors.Is(err, shop.ErrNotFound) {
		s.Metrics.Logins.WithLabelValues("invalid").Inc()
		writeError(w, http.StatusUnauthorized, "invalid_credentials", shop.ErrInvalidCredentials.Error())
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if err := s.CheckPassword(u.PasswordHash, req.Password); err != nil {
		s.Metrics.Logins.WithLabelValues("invalid").Inc()
		writeError(w, http.StatusUnauthorized, "invalid_credentials", shop.ErrInvalidCredentials.Error())
		return
	}
	sess, err := s.Sessions.Create(u.ID, u.Email)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.Metrics.Logins.WithLabelValues("ok").Inc()
	writeJSON(w, http.StatusOK, loginResponse{Token: sess.Token, TokenType: "Bearer", ExpiresAt: sess.ExpiresAt.UTC()})
}

func (s *server) me(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	u, err := s.Users.UserByID(r.Context(), sess.UserID)
	if errors.Is(err, shop.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "user no longer exists")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// ------------------------------------------------------------------- cart

func (s *server) cartView(ctx context.Context, cartID string) (shop.CartView, error) {
	view := shop.CartView{Items: []shop.CartItem{}}
	lines, err := s.Carts.Lines(ctx, cartID)
	if err != nil || len(lines) == 0 {
		return view, err
	}
	ids := make([]int64, len(lines))
	for i, l := range lines {
		ids[i] = l.ProductID
	}
	briefs, err := s.Catalog.ProductBriefs(ctx, ids)
	if err != nil {
		return view, err
	}
	for _, l := range lines {
		b, ok := briefs[l.ProductID]
		if !ok {
			continue
		}
		line := b.PriceCents * int64(l.Qty)
		view.Items = append(view.Items, shop.CartItem{
			ProductID: b.ID, Name: b.Name, Qty: l.Qty, PriceCents: b.PriceCents,
			LineTotalCents: line, LineTotal: shop.Dollars(line),
		})
		view.ItemCount += l.Qty
		view.SubtotalCents += line
	}
	slices.SortFunc(view.Items, func(a, b shop.CartItem) int { return int(a.ProductID - b.ProductID) })
	view.Subtotal = shop.Dollars(view.SubtotalCents)
	return view, nil
}

func (s *server) getCart(w http.ResponseWriter, r *http.Request) {
	view, err := s.cartView(r.Context(), sessionFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

type cartRequest struct {
	ProductID int64 `json:"productId"`
	Qty       int   `json:"qty"`
}

func (s *server) putCart(w http.ResponseWriter, r *http.Request) {
	var req cartRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ProductID < 1 {
		writeError(w, http.StatusBadRequest, "validation_failed", "productId must be a positive integer")
		return
	}
	if req.Qty < 1 || req.Qty > MaxCartQty {
		writeError(w, http.StatusBadRequest, "validation_failed", "qty must be between 1 and 99")
		return
	}
	briefs, err := s.Catalog.ProductBriefs(r.Context(), []int64{req.ProductID})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if _, ok := briefs[req.ProductID]; !ok {
		writeError(w, http.StatusNotFound, "not_found", "product not found")
		return
	}
	sess := sessionFrom(r.Context())
	if err := s.Carts.SetQty(r.Context(), sess.ID, req.ProductID, req.Qty); err != nil {
		s.internal(w, r, err)
		return
	}
	view, err := s.cartView(r.Context(), sess.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) deleteCartItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "productId")
	if !ok {
		return
	}
	sess := sessionFrom(r.Context())
	if err := s.Carts.Remove(r.Context(), sess.ID, id); err != nil {
		s.internal(w, r, err)
		return
	}
	view, err := s.cartView(r.Context(), sess.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// --------------------------------------------------------------- checkout

type checkoutResponse struct {
	OrderID    int64     `json:"orderId"`
	Total      float64   `json:"total"`
	TotalCents int64     `json:"totalCents"`
	ItemCount  int       `json:"itemCount"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (s *server) checkout(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	lines, err := s.Carts.Lines(r.Context(), sess.ID)
	if err != nil {
		s.Metrics.Checkouts.WithLabelValues("error").Inc()
		s.internal(w, r, err)
		return
	}
	order, err := s.Orders.Checkout(r.Context(), sess.UserID, lines)
	var oos *shop.OutOfStockError
	switch {
	case errors.Is(err, shop.ErrEmptyCart):
		s.Metrics.Checkouts.WithLabelValues("empty_cart").Inc()
		writeError(w, http.StatusBadRequest, "empty_cart", "cart is empty; add items with POST /api/cart first")
		return
	case errors.As(err, &oos):
		s.Metrics.Checkouts.WithLabelValues("out_of_stock").Inc()
		writeJSON(w, http.StatusConflict, ErrorBody{Error: ErrorDetail{
			Code: "out_of_stock", Message: oos.Error(),
			Details: map[string]any{"productId": oos.ProductID, "requested": oos.Requested, "available": oos.Available},
		}})
		return
	case err != nil:
		s.Metrics.Checkouts.WithLabelValues("error").Inc()
		s.internal(w, r, err)
		return
	}
	s.Metrics.Checkouts.WithLabelValues("ok").Inc()
	if err := s.Carts.Clear(r.Context(), sess.ID); err != nil {
		// The order exists; a stale cart is not worth failing the request.
		s.Logger.Warn("clear cart after checkout", "err", err, "order_id", order.ID)
	}
	writeJSON(w, http.StatusCreated, checkoutResponse{
		OrderID: order.ID, Total: order.Total, TotalCents: order.TotalCents,
		ItemCount: order.ItemCount, Status: order.Status, CreatedAt: order.CreatedAt.UTC(),
	})
}

// ----------------------------------------------------------------- orders

func (s *server) listOrders(w http.ResponseWriter, r *http.Request) {
	page, ok := queryInt(w, r, "page", 1)
	if !ok {
		return
	}
	perPage, ok := queryInt(w, r, "per_page", 20)
	if !ok {
		return
	}
	if perPage > 100 {
		writeError(w, http.StatusBadRequest, "invalid_query", "per_page must be between 1 and 100")
		return
	}
	res, err := s.Orders.ListOrders(r.Context(), sessionFrom(r.Context()).UserID, page, perPage)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) getOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	o, err := s.Orders.Order(r.Context(), sessionFrom(r.Context()).UserID, id)
	if errors.Is(err, shop.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "order not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// ------------------------------------------------------------------ admin

func (s *server) oversold(w http.ResponseWriter, r *http.Request) {
	products, err := s.Inventory.Oversold(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	var units int64
	for _, p := range products {
		units += p.OversoldUnits
	}
	if products == nil {
		products = []shop.OversoldProduct{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"oversoldUnits":      units,
		"detectedSinceStart": s.OversoldDetected(),
		"raceFixEnabled":     s.Fixes.Race,
		"products":           products,
	})
}

func (s *server) flushCache(w http.ResponseWriter, r *http.Request) {
	if s.FlushCache == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "no cache configured")
		return
	}
	n, err := s.FlushCache(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"flushed": n})
}

func (s *server) resetStock(w http.ResponseWriter, r *http.Request) {
	n, err := s.Inventory.ResetLowStock(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reset": n, "stock": shop.LowStockQty})
}
