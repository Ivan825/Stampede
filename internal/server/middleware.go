package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/store"
)

// apiError is returned by handlers and rendered as the standard error body.
type apiError struct {
	status  int
	code    string
	msg     string
	details []string
}

func (e *apiError) Error() string { return e.msg }

func errNotFound(what string) error {
	return &apiError{status: 404, code: "not_found", msg: what + " not found"}
}
func errForbidden(msg string) error { return &apiError{status: 403, code: "forbidden", msg: msg} }
func errConflict(msg string) error  { return &apiError{status: 409, code: "conflict", msg: msg} }
func errInvalid(msg string, details ...string) error {
	return &apiError{status: 422, code: "invalid", msg: msg, details: details}
}
func errUnauthorized(msg string) error { return &apiError{status: 401, code: "unauthorized", msg: msg} }
func errTooMany(msg string) error      { return &apiError{status: 429, code: "too_many_attempts", msg: msg} }

// notFoundOr maps a missing row to a 404 and passes other errors through.
func notFoundOr(err error, what string) error {
	if store.IsNotFound(err) {
		return errNotFound(what)
	}
	return err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, e *apiError) {
	body := map[string]any{"code": e.code, "message": e.msg}
	if len(e.details) > 0 {
		body["details"] = e.details
	}
	writeJSON(w, e.status, map[string]any{"error": body})
}

func (s *Server) requestError(w http.ResponseWriter, _ *http.Request, err error) {
	writeError(w, &apiError{status: 400, code: "bad_request", msg: err.Error()})
}

func (s *Server) responseError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		writeError(w, ae)
		return
	}
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err, "request_id", middleware.GetReqID(r.Context()))
	writeError(w, &apiError{status: 500, code: "internal", msg: "internal error (request " + middleware.GetReqID(r.Context()) + ")"})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				s.log.Error("panic", "error", fmt.Sprint(rec), "stack", string(debug.Stack()))
				writeError(w, &apiError{status: 500, code: "internal", msg: "internal error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			// The UI and exported HTML reports use inline styles only; no
			// external sources are ever loaded.
			h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; worker-src 'self' blob:; script-src 'self' blob:; connect-src 'self'; frame-ancestors 'none'")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		if strings.HasSuffix(r.URL.Path, "/live") {
			return
		}
		level := s.log.Debug
		if ww.Status() >= 500 {
			level = s.log.Error
		}
		level("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "took", time.Since(start))
	})
}

// SessionCookie is the name of the login cookie.
const SessionCookie = "stampede_session"

// publicPaths need no authentication.
var publicPaths = map[string]bool{
	"/api/v1/version":      true,
	"/api/v1/setup":        true,
	"/api/v1/auth/login":   true,
	"/api/v1/openapi.yaml": true,
}

// authenticate resolves the caller from a bearer token or session cookie.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.principal(r)
		if err != nil {
			writeError(w, &apiError{status: 401, code: "unauthorized", msg: err.Error()})
			return
		}
		if p == nil && !publicPaths[r.URL.Path] {
			writeError(w, &apiError{status: 401, code: "unauthorized", msg: "sign in or send an API token"})
			return
		}
		if p != nil {
			r = r.WithContext(auth.WithPrincipal(r.Context(), p))
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) principal(r *http.Request) (*auth.Principal, error) {
	ctx := r.Context()
	if h := r.Header.Get("Authorization"); h != "" {
		tok, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || !strings.HasPrefix(tok, auth.APITokenPrefix) {
			return nil, errors.New("malformed Authorization header: send Bearer followed by an stp_ token")
		}
		row, err := s.st.GetTokenByHash(ctx, auth.HashToken(tok))
		if err != nil {
			if store.IsNotFound(err) {
				return nil, errors.New("API token is invalid, revoked or expired")
			}
			return nil, err
		}
		_ = s.st.TouchToken(ctx, row.ID)
		return &auth.Principal{
			UserID: row.UserID, OrgID: row.OrgID, Email: row.Email, Name: row.UserName, OrgName: row.OrgName,
			Role: auth.Lower(auth.Role(row.TokenRole), auth.Role(row.UserRole)), Via: "token:" + row.Name,
		}, nil
	}
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	hash := auth.HashToken(c.Value)
	row, err := s.st.GetSession(ctx, hash)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, nil // expired; treat as signed out
		}
		return nil, err
	}
	return &auth.Principal{
		UserID: row.UserID, OrgID: row.OrgID, Email: row.Email, Name: row.Name, OrgName: row.OrgName,
		Role: auth.Role(row.Role), Via: "session", SessionHash: hash,
	}, nil
}

// csrf requires a custom header on state-changing requests that are not
// authenticated by a bearer token. Browsers cannot add custom headers to
// cross-site requests without a CORS preflight, which this server never
// grants, so a forged form post or image request is rejected.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get("Authorization") == "" && r.Header.Get("X-Stampede-CSRF") != "1" {
				writeError(w, &apiError{status: 403, code: "csrf", msg: "missing X-Stampede-CSRF header"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// need returns the principal when it holds at least role min.
func need(ctx context.Context, min auth.Role) (*auth.Principal, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errUnauthorized("sign in first")
	}
	if !p.Can(min) {
		return nil, errForbidden(fmt.Sprintf("this needs the %s role or higher; you are %s", min, p.Role))
	}
	return p, nil
}

type httpCtxKey struct{}

type httpPair struct {
	w http.ResponseWriter
	r *http.Request
}

// withHTTP is a strict-server middleware that exposes the raw request and
// response writer to handlers that set cookies or read client details.
func withHTTP(f gen.StrictHandlerFunc, _ string) gen.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		return f(context.WithValue(ctx, httpCtxKey{}, httpPair{w, r}), w, r, req)
	}
}

func httpFrom(ctx context.Context) (http.ResponseWriter, *http.Request) {
	p, _ := ctx.Value(httpCtxKey{}).(httpPair)
	return p.w, p.r
}

// clientIP is the caller's address (after RealIP processing).
func clientIP(ctx context.Context) string {
	_, r := httpFrom(ctx)
	if r == nil {
		return ""
	}
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return ap.Addr().String()
	}
	return r.RemoteAddr
}

// trustedProxies sets r.RemoteAddr to the real client address. Forwarding
// headers are only believed when the connection comes from a configured
// proxy; the rightmost address that is not itself a trusted proxy is the
// client. Believing them from anyone would let clients spoof their address
// and dodge per-address login throttling.
func trustedProxies(trusted []netip.Prefix) func(http.Handler) http.Handler {
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a.Unmap()) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(trusted) > 0 {
				if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil && isTrusted(ap.Addr()) {
					hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
					for i := len(hops) - 1; i >= 0; i-- {
						a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
						if err != nil {
							break
						}
						if !isTrusted(a) {
							r.RemoteAddr = netip.AddrPortFrom(a, 0).String()
							break
						}
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
