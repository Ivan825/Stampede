// Package api is ShopLab's HTTP layer: a chi router with JSON handlers,
// bearer-token auth, Prometheus instrumentation and the admin endpoints used
// by the bottleneck demos.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/crypto/bcrypt"

	"github.com/Ivan825/Stampede/examples/shoplab/internal/cache"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/config"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/metrics"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/session"
	"github.com/Ivan825/Stampede/examples/shoplab/internal/shop"
)

// Deps are the handler dependencies. Interfaces let tests use fakes.
type Deps struct {
	Catalog   shop.Catalog
	Users     shop.Users
	Orders    shop.Orders
	Inventory shop.Inventory
	Carts     shop.Carts
	Sessions  *session.Store
	Cache     *cache.Cache // nil: product detail is loaded on every request
	Metrics   *metrics.Metrics
	Logger    *slog.Logger

	Fixes   config.Fixes
	Admin   bool // enables mutating /admin endpoints
	Version string
	OpenAPI []byte

	// FlushCache drops every product-detail cache entry.
	FlushCache func(ctx context.Context) (int, error)
	// Ready runs readiness checks; a non-nil error marks a dependency down.
	Ready map[string]func(ctx context.Context) error
	// OversoldDetected returns units oversold since process start.
	OversoldDetected func() int64
	// CheckPassword compares a hash with a password (bcrypt by default).
	CheckPassword func(hash, password string) error
}

type server struct{ Deps }

// NewRouter builds the HTTP handler.
func NewRouter(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.Metrics == nil {
		d.Metrics = metrics.New(d.Fixes.Map())
	}
	if d.CheckPassword == nil {
		d.CheckPassword = func(hash, pw string) error {
			return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw))
		}
	}
	if d.OversoldDetected == nil {
		d.OversoldDetected = func() int64 { return 0 }
	}
	s := &server{d}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(s.instrument)
	r.Use(s.recoverer)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no route for "+r.Method+" "+r.URL.Path)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" is not allowed on "+r.URL.Path)
	})

	r.Get("/", s.index)
	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.readyz)
	r.Method(http.MethodGet, "/metrics", promhttp.HandlerFor(d.Metrics.Registry, promhttp.HandlerOpts{}))
	r.Get("/openapi.yaml", s.openapi)

	r.Route("/api", func(r chi.Router) {
		r.Get("/products", s.listProducts)
		r.Get("/products/{id}", s.getProduct)
		r.Post("/login", s.login)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/me", s.me)
			r.Get("/cart", s.getCart)
			r.Post("/cart", s.putCart)
			r.Delete("/cart/{productId}", s.deleteCartItem)
			r.Post("/checkout", s.checkout)
			r.Get("/orders", s.listOrders)
			r.Get("/orders/{id}", s.getOrder)
		})
	})

	r.Route("/admin", func(r chi.Router) {
		r.Get("/oversold", s.oversold)
		r.Group(func(r chi.Router) {
			r.Use(s.requireAdmin)
			r.Post("/cache/flush", s.flushCache)
			r.Post("/stock/reset", s.resetStock)
		})
	})
	return r
}

// ---------------------------------------------------------------- helpers

// ErrorBody is the JSON shape of every error response.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail carries a stable machine code and a human message.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: msg}})
}

func (s *server) internal(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		// Client went away; nothing useful to send.
		w.WriteHeader(499)
		return
	}
	s.Logger.Error("request failed", "method", r.Method, "path", r.URL.Path,
		"request_id", middleware.GetReqID(r.Context()), "err", err)
	if errors.Is(err, context.DeadlineExceeded) {
		writeError(w, http.StatusServiceUnavailable, "timeout", "the request timed out")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal", "internal server error")
}

const maxBody = 1 << 20

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON: "+err.Error())
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid_id", name+" must be a positive integer")
		return 0, false
	}
	return id, true
}

func queryInt(w http.ResponseWriter, r *http.Request, name string, def int) (int, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		writeError(w, http.StatusBadRequest, "invalid_query", name+" must be a positive integer")
		return 0, false
	}
	return n, true
}

// ------------------------------------------------------------- middleware

func (s *server) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		s.Metrics.HTTPInFlight.Inc()
		defer func() {
			s.Metrics.HTTPInFlight.Dec()
			d := time.Since(start)
			route := "unmatched"
			if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
				route = rc.RoutePattern()
			}
			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			s.Metrics.ObserveHTTP(route, r.Method, strconv.Itoa(status), d)
			s.Logger.Debug("request", "method", r.Method, "route", route, "path", r.URL.Path,
				"status", status, "duration_ms", float64(d.Microseconds())/1000,
				"request_id", middleware.GetReqID(r.Context()))
		}()
		next.ServeHTTP(ww, r)
	})
}

func (s *server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				s.Logger.Error("panic", "path", r.URL.Path, "panic", rec)
				writeError(w, http.StatusInternalServerError, "internal", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type ctxKey struct{}

func sessionFrom(ctx context.Context) *session.Session {
	sess, _ := ctx.Value(ctxKey{}).(*session.Session)
	return sess
}

func (s *server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(h) <= len(prefix) || !equalFold(h[:len(prefix)], prefix) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="shoplab"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		sess, ok := s.Sessions.Get(h[len(prefix):])
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="shoplab", error="invalid_token"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, sess)))
	})
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func (s *server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Admin {
			writeError(w, http.StatusNotFound, "not_found", "admin endpoints are disabled; set SHOPLAB_ADMIN=1")
			return
		}
		next.ServeHTTP(w, r)
	})
}
