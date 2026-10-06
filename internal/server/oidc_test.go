package server_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/server"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

// fakeIdP is a minimal OpenID Connect provider that signs in whoever
// the test says, without a login page.
type fakeIdP struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	mu       sync.Mutex
	email    string
	verified bool
	codes    map[string]string // code -> nonce
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{key: key, codes: map[string]string{}, verified: true}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token",
			"jwks_uri": f.srv.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "PKCE required", 400)
			return
		}
		code := "code-" + q.Get("state")
		f.mu.Lock()
		f.codes[code] = q.Get("nonce")
		f.mu.Unlock()
		back, _ := url.Parse(q.Get("redirect_uri"))
		v := back.Query()
		v.Set("code", code)
		v.Set("state", q.Get("state"))
		back.RawQuery = v.Encode()
		http.Redirect(w, r, back.String(), http.StatusFound)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		nonce, ok := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code"))
		email, verified := f.email, f.verified
		f.mu.Unlock()
		if !ok || r.Form.Get("code_verifier") == "" {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		sig, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
		claims := map[string]any{
			"iss": f.srv.URL, "aud": "stampede", "sub": "user-" + email, "email": email, "email_verified": verified,
			"name": "Ana Example", "nonce": nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		}
		tok, _ := jwt.Signed(sig).Claims(claims).Serialize()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": tok, "expires_in": 3600})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) signIn(email string, verified bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.email, f.verified = email, verified
}

func TestOIDCSignIn(t *testing.T) {
	idp := newFakeIdP(t)
	st := storetest.Open(t)
	key, _ := keyringKey()
	hs := httptest.NewUnstartedServer(nil)
	base := "http://" + hs.Listener.Addr().String()
	srv, err := server.New(server.Config{
		Store: st, Keyring: key, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notify: server.NotifyConfig{PublicURL: base},
		OIDC: &server.OIDCConfig{Issuer: idp.srv.URL, ClientID: "stampede", ClientSecret: "s", Name: "Acme SSO",
			AllowedDomains: []string{"acme.test"}, DefaultRole: auth.RoleViewer},
	})
	if err != nil {
		t.Fatal(err)
	}
	hs.Config.Handler = srv.Handler()
	hs.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		hs.Close()
	})

	admin := newClient(t, base)
	setup(t, admin)
	var cfg map[string]any
	admin.do("GET", "/auth/config", nil, &cfg)
	if sso, _ := cfg["sso"].(map[string]any); sso["name"] != "Acme SSO" || sso["loginURL"] != "/api/v1/auth/oidc/login" {
		t.Fatalf("auth config = %v", cfg)
	}

	// signIn drives the browser flow and returns where it ended and the
	// signed-in user, if any.
	signIn := func(email string, verified bool) (string, map[string]any) {
		idp.signIn(email, verified)
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if req.URL.Host == hs.Listener.Addr().String() && !strings.HasPrefix(req.URL.Path, "/api/") {
				return http.ErrUseLastResponse // back in the app
			}
			return nil
		}}
		resp, err := c.Get(base + "/api/v1/auth/oidc/login?next=/runs")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		me, _ := c.Get(base + "/api/v1/me")
		var out map[string]any
		if me.StatusCode == 200 {
			_ = json.NewDecoder(me.Body).Decode(&out)
		}
		me.Body.Close()
		return loc, out
	}

	loc, me := signIn("Ana@acme.test", true)
	if loc != "/runs" || me["email"] != "ana@acme.test" || me["role"] != "viewer" || me["name"] != "Ana Example" {
		t.Fatalf("first sign-in: location %q, me %v", loc, me)
	}
	// The second sign-in finds the same account, with the role an admin gave it.
	var users []map[string]any
	admin.do("GET", "/users", nil, &users)
	for _, u := range users {
		if u["email"] == "ana@acme.test" {
			admin.do("PATCH", "/users/"+u["id"].(string), map[string]any{"role": "editor"}, nil)
		}
	}
	if _, me := signIn("ana@acme.test", true); me["role"] != "editor" {
		t.Errorf("second sign-in: %v", me)
	}
	for _, c := range []struct {
		email    string
		verified bool
		want     string
	}{
		{"bob@elsewhere.test", true, "may not sign in here"},
		{"carl@acme.test", false, "verified email"},
	} {
		loc, me := signIn(c.email, c.verified)
		if me != nil || !strings.HasPrefix(loc, "/login?sso_error=") || !strings.Contains(mustUnescape(loc), c.want) {
			t.Errorf("%s: location %q me %v", c.email, loc, me)
		}
	}
	// An SSO account cannot sign in with a password.
	var e errBody
	if code := newClient(t, base).do("POST", "/auth/login", map[string]string{"email": "ana@acme.test", "password": "!sso"}, &e); code != 401 {
		t.Errorf("password sign-in for an SSO account: %d", code)
	}
}

func mustUnescape(s string) string {
	u, _ := url.QueryUnescape(s)
	return u
}
