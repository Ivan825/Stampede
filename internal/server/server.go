// Package server is the Stampede control plane: the REST API, the run
// manager, live streams and the embedded web UI.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/keyring"
	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/store"
)

// Config configures the server.
type Config struct {
	Store   *store.Store
	Keyring *keyring.Keyring
	Logger  *slog.Logger
	// Executor runs load. The default runs it inside the server process.
	Executor Executor
	// Workers lists connected workers for GET /workers (optional).
	Workers func() []WorkerInfo
	// UI is the built web app; nil serves only the API.
	UI fs.FS
	// SecureCookies sets the Secure flag (enable behind HTTPS).
	SecureCookies bool
	// SessionTTL is how long a login lasts (default 7 days).
	SessionTTL time.Duration
	// HardCaps bound every run regardless of target settings.
	HardCaps safety.Caps
	// AbortFloor stops any run whose target is clearly failing, even when
	// its scenario sets no abort limits. Nil disables it.
	AbortFloor *scenario.Abort
	// DataDir is where CSV and JSON feeder files for server runs live.
	// Empty disables file feeders on the server.
	DataDir string
	// TrustedProxies are the reverse proxies whose X-Forwarded-For header
	// is believed. Empty means the connection's address is the client.
	TrustedProxies []netip.Prefix
	// Clock is replaceable in tests.
	Now func() time.Time
	// AI configures optional AI journey generation (see handlers_ai.go).
	AI AIConfig
	// Notify configures run notifications (see notifications.go).
	Notify NotifyConfig
	// OIDC turns on single sign-on (see oidc.go).
	OIDC *OIDCConfig
	// SchedulerInterval is how often StartScheduler looks for due
	// schedules (default 15s).
	SchedulerInterval time.Duration
}

// Server is the control plane.
type Server struct {
	cfg       Config
	oidc      *oidcSSO
	st        *store.Store
	log       *slog.Logger
	runs      *runManager
	ai        *aiManager
	notify    *notifier
	sched     *scheduler
	limiter   *auth.Limiter // per email
	ipLimiter *auth.Limiter // per client address, looser for shared NATs
	reg       *prometheus.Registry
	httpm     *httpMetrics
	handler   http.Handler
}

// New builds a server. Call Recover once at start-up to settle runs left
// unfinished by a previous process.
func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, errors.New("server: store is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 7 * 24 * time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.SchedulerInterval <= 0 {
		cfg.SchedulerInterval = DefaultSchedulerInterval
	}
	if cfg.Executor == nil {
		cfg.Executor = &LocalExecutor{Logger: cfg.Logger}
	}
	s := &Server{
		cfg:       cfg,
		st:        cfg.Store,
		log:       cfg.Logger,
		limiter:   auth.NewLimiter(10, 15*time.Minute),
		ipLimiter: auth.NewLimiter(100, 15*time.Minute),
		reg:       prometheus.NewRegistry(),
	}
	s.reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	s.runs = newRunManager(s)
	s.ai = newAIManager(s)
	s.notify = newNotifier(s)
	if o := cfg.OIDC; o != nil {
		if o.Issuer == "" || o.ClientID == "" {
			return nil, errors.New("OIDC needs an issuer and a client ID")
		}
		if o.Name == "" {
			o.Name = "SSO"
		}
		if o.RedirectURL == "" {
			if cfg.Notify.PublicURL == "" {
				return nil, errors.New("OIDC needs the server's public URL (--public-url) or an explicit redirect URL")
			}
			o.RedirectURL = strings.TrimRight(cfg.Notify.PublicURL, "/") + "/api/v1/auth/oidc/callback"
		}
		if o.DefaultRole != "" && !o.DefaultRole.Valid() {
			return nil, fmt.Errorf("OIDC default role %q is not a role", o.DefaultRole)
		}
		if o.DefaultRole == auth.RoleOwner {
			return nil, errors.New("OIDC sign-ins cannot be made owners automatically")
		}
		for i, d := range o.AllowedDomains {
			o.AllowedDomains[i] = strings.ToLower(strings.TrimSpace(d))
		}
		s.oidc = &oidcSSO{cfg: *o, flows: map[string]oidcFlow{}}
	}
	s.reg.MustRegister(s.runs.metrics()...)
	s.httpm = newHTTPMetrics()
	s.reg.MustRegister(s.httpm.collectors()...)
	s.handler = s.routes()
	return s, nil
}

// Handler is the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// Recover marks runs that were in progress when the server last stopped.
func (s *Server) Recover(ctx context.Context) error {
	if err := s.ai.recover(ctx); err != nil {
		return err
	}
	return s.runs.recover(ctx)
}

// Shutdown stops the scheduler and every active run, and waits for
// reports to be written.
func (s *Server) Shutdown(ctx context.Context) {
	s.stopScheduler()
	s.ai.shutdown(ctx)
	s.runs.shutdown(ctx)
	s.notify.shutdown(ctx)
}

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(trustedProxies(s.cfg.TrustedProxies), middleware.RequestID, s.recoverer, securityHeaders(contentSecurityPolicy(s.cfg.UI)))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.st.Pool.Ping(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok\n"))
	})
	r.Handle("/metrics", promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{Registry: s.reg}))

	r.Route("/api/v1", func(api chi.Router) {
		api.Use(s.instrument, s.logRequests, s.authenticate, s.csrf)
		api.Get("/openapi.yaml", s.serveSpec)
		strict := gen.NewStrictHandlerWithOptions(&handlers{s}, []gen.StrictMiddlewareFunc{withHTTP}, gen.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  s.requestError,
			ResponseErrorHandlerFunc: s.responseError,
		})
		gen.HandlerWithOptions(strict, gen.ChiServerOptions{
			BaseRouter:       api,
			ErrorHandlerFunc: s.requestError,
		})
		// Live streams are written by hand because the generated strict
		// server buffers responses, which does not suit server-sent events.
		// Registered last so it replaces the generated route.
		api.Get("/runs/{runId}/live", s.streamRun)
		// Single sign-on redirects, also outside the strict server.
		api.Get("/auth/oidc/login", s.oidcLogin)
		api.Get("/auth/oidc/callback", s.oidcCallback)
	})

	if s.cfg.UI != nil {
		r.Handle("/*", spaHandler(s.cfg.UI))
	}
	return r
}

func (s *Server) serveSpec(w http.ResponseWriter, _ *http.Request) {
	spec, err := gen.GetSpec()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	b, _ := spec.MarshalJSON()
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// spaHandler serves static files and falls back to index.html so client
// side routes work on reload.
func spaHandler(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(ui, p); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

// ListenAndServe runs the HTTP server until ctx ends, then shuts down
// gracefully: active runs are stopped and their reports written.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	hs := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	errCh := make(chan error, 1)
	go func() { errCh <- hs.Serve(ln) }()
	s.log.Info("stampede server listening", "addr", ln.Addr().String())
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s.Shutdown(shutCtx)
	if err := hs.Shutdown(shutCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// handlers implements the generated strict server interface.
type handlers struct{ *Server }

var _ gen.StrictServerInterface = (*handlers)(nil)
