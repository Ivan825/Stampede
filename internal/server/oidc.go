package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
)

// OIDCConfig turns on single sign-on with an OpenID Connect provider
// (Okta, Entra ID, Google, Keycloak, Auth0, Dex ...).
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	// RedirectURL is where the provider sends users back; default the
	// server's public URL + /api/v1/auth/oidc/callback.
	RedirectURL string
	// Name labels the sign-in button (default "SSO").
	Name string
	// AllowedDomains limits sign-in to these email domains (empty: any).
	AllowedDomains []string
	// DefaultRole is given to people who sign in for the first time; empty
	// means only people who already have an account may sign in.
	DefaultRole auth.Role
	// Scopes besides openid (default email and profile).
	Scopes []string
}

const (
	oidcStateCookie = "stampede_oidc"
	oidcFlowTTL     = 10 * time.Minute
)

type oidcFlow struct {
	nonce, verifier, next string
	expires               time.Time
}

// oidcSSO is the server's OIDC client. The provider is discovered on first
// use, so a server can start while its identity provider is unreachable.
type oidcSSO struct {
	cfg OIDCConfig

	once     sync.Once
	err      error
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config

	mu    sync.Mutex
	flows map[string]oidcFlow
}

func (o *oidcSSO) init(ctx context.Context) error {
	o.once.Do(func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		p, err := oidc.NewProvider(dctx, o.cfg.Issuer)
		if err != nil {
			o.err = fmt.Errorf("discover %s: %w", o.cfg.Issuer, err)
			return
		}
		scopes := append([]string{oidc.ScopeOpenID}, o.cfg.Scopes...)
		if len(o.cfg.Scopes) == 0 {
			scopes = append(scopes, "email", "profile")
		}
		o.provider = p
		o.verifier = p.Verifier(&oidc.Config{ClientID: o.cfg.ClientID})
		o.oauth = &oauth2.Config{ClientID: o.cfg.ClientID, ClientSecret: o.cfg.ClientSecret, Endpoint: p.Endpoint(), RedirectURL: o.cfg.RedirectURL, Scopes: scopes}
	})
	if o.err != nil {
		// Retry discovery later instead of failing forever.
		o.once = sync.Once{}
	}
	return o.err
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// safeNext keeps a post-login redirect on this site.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\\\") {
		return "/"
	}
	return next
}

// oidcLogin starts sign-in: GET /api/v1/auth/oidc/login?next=/path.
func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	o := s.oidc
	if o == nil {
		writeError(w, &apiError{status: 404, code: "not_found", msg: "single sign-on is not configured"})
		return
	}
	if err := o.init(r.Context()); err != nil {
		s.log.Error("OIDC discovery failed", "error", err)
		writeError(w, &apiError{status: 502, code: "sso_unavailable", msg: "the identity provider is unreachable; try again shortly"})
		return
	}
	state, nonce, verifier := randomToken(), randomToken(), oauth2.GenerateVerifier()
	o.mu.Lock()
	now := s.cfg.Now()
	for k, f := range o.flows {
		if now.After(f.expires) {
			delete(o.flows, k)
		}
	}
	if len(o.flows) > 10000 {
		o.mu.Unlock()
		writeError(w, &apiError{status: 429, code: "too_many", msg: "too many sign-ins in progress; try again shortly"})
		return
	}
	o.flows[state] = oidcFlow{nonce: nonce, verifier: verifier, next: safeNext(r.URL.Query().Get("next")), expires: now.Add(oidcFlowTTL)}
	o.mu.Unlock()
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is configurable for plain-HTTP local installs
		Name: oidcStateCookie, Value: state, Path: "/api/v1/auth/oidc", MaxAge: int(oidcFlowTTL.Seconds()),
		HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, o.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

// oidcCallback finishes sign-in: the provider redirects here with a code.
func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	o := s.oidc
	if o == nil {
		writeError(w, &apiError{status: 404, code: "not_found", msg: "single sign-on is not configured"})
		return
	}
	fail := func(status int, msg string, err error) {
		if err != nil {
			s.log.Warn("SSO sign-in refused", "reason", msg, "error", err)
		}
		http.Redirect(w, r, "/login?sso_error="+url.QueryEscape(msg), http.StatusFound)
		_ = status
	}
	if e := r.URL.Query().Get("error"); e != "" {
		fail(400, "the identity provider refused the sign-in ("+e+")", nil)
		return
	}
	c, err := r.Cookie(oidcStateCookie)
	state := r.URL.Query().Get("state")
	if err != nil || state == "" || c.Value != state {
		fail(400, "the sign-in expired or came from another browser; start again", err)
		return
	}
	o.mu.Lock()
	flow, ok := o.flows[state]
	delete(o.flows, state)
	o.mu.Unlock()
	if !ok || s.cfg.Now().After(flow.expires) {
		fail(400, "the sign-in expired; start again", nil)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: "", Path: "/api/v1/auth/oidc", MaxAge: -1}) //nolint:gosec // clearing
	if err := o.init(r.Context()); err != nil {
		fail(502, "the identity provider is unreachable", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tok, err := o.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(flow.verifier))
	if err != nil {
		fail(400, "the sign-in code was not accepted", err)
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := o.verifier.Verify(ctx, raw)
	if err != nil {
		fail(400, "the identity token is not valid", err)
		return
	}
	if idt.Nonce != flow.nonce {
		fail(400, "the identity token does not belong to this sign-in", errors.New("nonce mismatch"))
		return
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idt.Claims(&claims); err != nil {
		fail(400, "the identity token has no email", err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if email == "" || (claims.EmailVerified != nil && !*claims.EmailVerified) {
		fail(403, "the identity provider did not give a verified email address", nil)
		return
	}
	if d := o.cfg.AllowedDomains; len(d) > 0 {
		_, domain, _ := strings.Cut(email, "@")
		if !slices.Contains(d, domain) {
			fail(403, "accounts from "+domain+" may not sign in here", nil)
			return
		}
	}
	p, created, err := s.ssoPrincipal(ctx, email, claims.Name)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) {
			fail(ae.status, ae.msg, nil)
			return
		}
		fail(500, "the sign-in failed", err)
		return
	}
	hctx := context.WithValue(ctx, httpCtxKey{}, httpPair{w, r})
	h := &handlers{s}
	if _, err := h.startSession(hctx, p); err != nil {
		fail(500, "the sign-in failed", err)
		return
	}
	if created {
		h.auditAs(hctx, p, "user.create", email, map[string]any{"via": "sso", "role": p.Role})
	}
	h.auditAs(hctx, p, "auth.login", email, map[string]any{"via": "sso"})
	http.Redirect(w, r, flow.next, http.StatusFound)
}

// ssoPrincipal finds the account for a verified email, or creates one with
// the default role when that is allowed.
func (s *Server) ssoPrincipal(ctx context.Context, email, name string) (*auth.Principal, bool, error) {
	u, err := s.st.GetUserByEmail(ctx, email)
	switch {
	case err == nil:
		m, err := s.st.GetMembershipForUser(ctx, u.ID)
		if err != nil {
			return nil, false, err
		}
		return &auth.Principal{UserID: u.ID, OrgID: m.OrgID, Email: u.Email, Name: u.Name, OrgName: m.OrgName, Role: auth.Role(m.Role), Via: "session"}, false, nil
	case !store.IsNotFound(err):
		return nil, false, err
	}
	role := s.oidc.cfg.DefaultRole
	if role == "" {
		return nil, false, &apiError{status: 403, code: "forbidden", msg: "there is no Stampede account for " + email + "; ask an admin to invite you"}
	}
	org, err := s.st.FirstOrg(ctx)
	if err != nil {
		return nil, false, &apiError{status: 409, code: "conflict", msg: "finish setup before signing in with SSO"}
	}
	if name == "" {
		name, _, _ = strings.Cut(email, "@")
	}
	id := uuid.New()
	err = s.st.InTx(ctx, func(q *db.Queries) error {
		// An unusable hash: SSO accounts sign in only through the provider
		// until an admin sets a password.
		if err := q.CreateUser(ctx, db.CreateUserParams{ID: id, Email: email, Name: name, PasswordHash: "!sso"}); err != nil {
			return err
		}
		return q.CreateMembership(ctx, db.CreateMembershipParams{OrgID: org.ID, UserID: id, Role: string(role)})
	})
	if store.IsUniqueViolation(err) {
		// Created concurrently by another sign-in: use that account.
		return s.ssoPrincipal(ctx, email, name)
	}
	if err != nil {
		return nil, false, err
	}
	return &auth.Principal{UserID: id, OrgID: org.ID, Email: email, Name: name, OrgName: org.Name, Role: role, Via: "session"}, true, nil
}

// GetAuthConfig tells the sign-in page which methods are available.
func (h *handlers) GetAuthConfig(context.Context, gen.GetAuthConfigRequestObject) (gen.GetAuthConfigResponseObject, error) {
	out := gen.AuthConfig{Password: true}
	if o := h.oidc; o != nil {
		out.Sso = &struct {
			LoginURL string `json:"loginURL"`
			Name     string `json:"name"`
		}{LoginURL: "/api/v1/auth/oidc/login", Name: o.cfg.Name}
	}
	return gen.GetAuthConfig200JSONResponse(out), nil
}
