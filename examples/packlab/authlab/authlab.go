// Package authlab is AuthLab, a small OAuth 2.0 and OpenID Connect token
// service: password, refresh-token and client-credentials grants, userinfo,
// introspection and revocation, with Ed25519-signed JWT access tokens. It is
// the reference app for the identity pack.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - lock: password hashes are checked while holding the store's single
//     lock, so logins run one at a time and refreshes wait behind them
//     (fix "lock" hashes outside the lock).
//   - index: refresh tokens live in a list that is never pruned and is
//     scanned with a constant-time compare on every refresh and
//     revocation, so each refresh costs more the more tokens were ever
//     issued (fix "index" uses a map and drops expired tokens).
package authlab

import (
	"crypto/ed25519"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

// Users is how many accounts AuthLab seeds: user0001@authlab.test to
// user1000@authlab.test, all with the password Password.
const (
	Users    = 1000
	Password = "authlab-pass"
)

const (
	accessTTL  = 5 * time.Minute
	refreshTTL = 24 * time.Hour
)

type user struct {
	sub, email, name string
	salt, hash       []byte
}

type refreshToken struct {
	token, sub, client, family string
	expires                    time.Time
	revoked                    bool
}

type client struct {
	secret string // empty for public clients
	grants []string
}

type server struct {
	cfg   labkit.Config
	iters int
	key   ed25519.PrivateKey
	kid   string

	mu       sync.Mutex
	users    map[string]*user
	list     []*refreshToken          // without the index fix
	index    map[string]*refreshToken // with the index fix
	clients  map[string]client
	scanned  labkit.Counter
	hashing  labkit.Gauge
	dummySlt []byte
}

// New returns AuthLab's handler.
func New(cfg labkit.Config) http.Handler {
	_, h := newServer(cfg)
	return h
}

func newServer(cfg labkit.Config) (*server, http.Handler) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	s := &server{cfg: cfg, iters: 20000, key: key, kid: labkit.Token("k")[:9],
		users: map[string]*user{}, index: map[string]*refreshToken{}, dummySlt: []byte("dummy-salt-0000")}
	if cfg.Fast {
		s.iters = 1000
	}
	s.clients = map[string]client{
		"web":               {grants: []string{"password", "refresh_token"}},
		"mobile":            {grants: []string{"password", "refresh_token"}},
		"reporting-service": {secret: "authlab-secret", grants: []string{"client_credentials"}},
	}
	s.seed()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "AuthLab", "links": map[string]string{"discovery": "/.well-known/openid-configuration"}})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /.well-known/openid-configuration", s.discovery)
	mux.HandleFunc("GET /.well-known/jwks.json", s.jwks)
	mux.HandleFunc("POST /oauth/token", s.token)
	mux.HandleFunc("POST /oauth/revoke", s.revoke)
	mux.HandleFunc("POST /oauth/introspect", s.introspect)
	mux.HandleFunc("GET /userinfo", s.userinfo)
	return s, mux
}

// seed creates the users, hashing passwords on every core.
func (s *server) seed() {
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				email := fmt.Sprintf("user%04d@authlab.test", i)
				salt := sha256.Sum256([]byte(email))
				u := &user{sub: fmt.Sprintf("u-%04d", i), email: email, name: fmt.Sprintf("User %d", i), salt: salt[:16]}
				u.hash = s.derive(Password, u.salt)
				mu.Lock()
				s.users[email] = u
				mu.Unlock()
			}
		}()
	}
	for i := 1; i <= Users; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
}

func (s *server) derive(password string, salt []byte) []byte {
	k, _ := pbkdf2.Key(sha256.New, password, salt, s.iters, 32)
	return k
}

// checkPassword compares a password with the user's hash. Unknown users
// still pay for one hash so response time does not reveal who exists.
func (s *server) checkPassword(u *user, password string) bool {
	defer s.hashing.Enter()()
	if u == nil {
		s.derive(password, s.dummySlt)
		return false
	}
	return subtle.ConstantTimeCompare(s.derive(password, u.salt), u.hash) == 1
}

func (s *server) issuer(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *server) discovery(w http.ResponseWriter, r *http.Request) {
	iss := s.issuer(r)
	labkit.JSON(w, 200, map[string]any{
		"issuer":                                iss,
		"authorization_endpoint":                iss + "/oauth/authorize",
		"token_endpoint":                        iss + "/oauth/token",
		"userinfo_endpoint":                     iss + "/userinfo",
		"jwks_uri":                              iss + "/.well-known/jwks.json",
		"revocation_endpoint":                   iss + "/oauth/revoke",
		"introspection_endpoint":                iss + "/oauth/introspect",
		"grant_types_supported":                 []string{"password", "refresh_token", "client_credentials"},
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"EdDSA"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "client_secret_basic", "none"},
	})
}

func (s *server) jwks(w http.ResponseWriter, _ *http.Request) {
	pub := s.key.Public().(ed25519.PublicKey)
	labkit.JSON(w, 200, map[string]any{"keys": []map[string]string{{
		"kty": "OKP", "crv": "Ed25519", "alg": "EdDSA", "use": "sig", "kid": s.kid,
		"x": base64.RawURLEncoding.EncodeToString(pub),
	}}})
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="authlab"`)
	}
	labkit.JSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// authClient identifies the client from Basic auth or the form.
func (s *server) authClient(r *http.Request) (string, client, bool) {
	id, secret, basic := r.BasicAuth()
	if !basic {
		id, secret = r.PostFormValue("client_id"), r.PostFormValue("client_secret")
	}
	c, ok := s.clients[id]
	if !ok || subtle.ConstantTimeCompare([]byte(c.secret), []byte(secret)) != 1 {
		return id, c, false
	}
	return id, c, true
}

func allows(c client, grant string) bool {
	for _, g := range c.grants {
		if g == grant {
			return true
		}
	}
	return false
}

func (s *server) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, 400, "invalid_request", "the body must be form-encoded")
		return
	}
	grant := r.PostFormValue("grant_type")
	id, c, ok := s.authClient(r)
	if !ok {
		oauthError(w, 401, "invalid_client", "unknown client or wrong secret")
		return
	}
	if !allows(c, grant) {
		oauthError(w, 400, "unsupported_grant_type", "grant "+grant+" is not allowed for this client")
		return
	}
	iss := s.issuer(r)
	switch grant {
	case "password":
		s.passwordGrant(w, r, iss, id)
	case "refresh_token":
		s.refreshGrant(w, r, iss, id)
	case "client_credentials":
		labkit.JSON(w, 200, map[string]any{
			"access_token": s.sign(map[string]any{"iss": iss, "sub": id, "client_id": id, "scope": "reports:read"}, accessTTL),
			"token_type":   "Bearer", "expires_in": int(accessTTL.Seconds()), "scope": "reports:read",
		})
	}
}

func (s *server) passwordGrant(w http.ResponseWriter, r *http.Request, iss, clientID string) {
	email := strings.ToLower(strings.TrimSpace(r.PostFormValue("username")))
	password := r.PostFormValue("password")
	var u *user
	var ok bool
	if s.cfg.Fixes.On("lock") {
		s.mu.Lock()
		u = s.users[email]
		s.mu.Unlock()
		ok = s.checkPassword(u, password)
	} else {
		// Bottleneck: the hash is checked while holding the store lock.
		s.mu.Lock()
		u = s.users[email]
		ok = s.checkPassword(u, password)
		s.mu.Unlock()
	}
	if !ok {
		oauthError(w, 400, "invalid_grant", "wrong username or password")
		return
	}
	rt := s.storeRefresh(&refreshToken{sub: u.sub, client: clientID, family: labkit.Token("f")})
	s.respondTokens(w, iss, u, clientID, rt)
}

func (s *server) refreshGrant(w http.ResponseWriter, r *http.Request, iss, clientID string) {
	presented := r.PostFormValue("refresh_token")
	s.mu.Lock()
	old := s.findRefresh(presented)
	switch {
	case old == nil || old.client != clientID || time.Now().After(old.expires):
		s.mu.Unlock()
		oauthError(w, 400, "invalid_grant", "unknown or expired refresh token")
		return
	case old.revoked:
		// Reuse of a rotated token: revoke the whole family.
		s.revokeFamily(old.family)
		s.mu.Unlock()
		oauthError(w, 400, "invalid_grant", "refresh token was already used")
		return
	}
	old.revoked = true
	u := s.userBySub(old.sub)
	s.mu.Unlock()
	rt := s.storeRefresh(&refreshToken{sub: old.sub, client: clientID, family: old.family})
	s.respondTokens(w, iss, u, clientID, rt)
}

func (s *server) respondTokens(w http.ResponseWriter, iss string, u *user, clientID, refresh string) {
	claims := map[string]any{"iss": iss, "sub": u.sub, "client_id": clientID, "scope": "openid profile email"}
	labkit.JSON(w, 200, map[string]any{
		"access_token":  s.sign(claims, accessTTL),
		"id_token":      s.sign(map[string]any{"iss": iss, "sub": u.sub, "aud": clientID, "email": u.email}, accessTTL),
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    int(accessTTL.Seconds()),
		"scope":         "openid profile email",
	})
}

func (s *server) userBySub(sub string) *user {
	var n int
	_, _ = fmt.Sscanf(sub, "u-%d", &n)
	return s.users[fmt.Sprintf("user%04d@authlab.test", n)]
}

// storeRefresh records a new refresh token and returns it.
func (s *server) storeRefresh(rt *refreshToken) string {
	rt.token = labkit.Token("rt_")
	rt.expires = time.Now().Add(refreshTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.Fixes.On("index") {
		s.index[rt.token] = rt
		if len(s.index)%1024 == 0 {
			now := time.Now()
			for k, v := range s.index {
				if v.revoked || now.After(v.expires) {
					delete(s.index, k)
				}
			}
		}
	} else {
		s.list = append(s.list, rt)
	}
	return rt.token
}

// findRefresh looks a token up; the caller holds s.mu.
func (s *server) findRefresh(tok string) *refreshToken {
	if s.cfg.Fixes.On("index") {
		return s.index[tok]
	}
	// Bottleneck: a linear, constant-time scan over every token ever issued.
	var found *refreshToken
	for _, rt := range s.list {
		if subtle.ConstantTimeCompare([]byte(rt.token), []byte(tok)) == 1 {
			found = rt
		}
	}
	s.scanned.Add(len(s.list))
	return found
}

func (s *server) revokeFamily(family string) {
	if s.cfg.Fixes.On("index") {
		for _, rt := range s.index {
			if rt.family == family {
				rt.revoked = true
			}
		}
		return
	}
	for _, rt := range s.list {
		if rt.family == family {
			rt.revoked = true
		}
	}
}

func (s *server) revoke(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, 400, "invalid_request", "the body must be form-encoded")
		return
	}
	id, _, ok := s.authClient(r)
	if !ok {
		oauthError(w, 401, "invalid_client", "unknown client or wrong secret")
		return
	}
	s.mu.Lock()
	if rt := s.findRefresh(r.PostFormValue("token")); rt != nil && rt.client == id {
		s.revokeFamily(rt.family)
	}
	s.mu.Unlock()
	// RFC 7009: answer 200 whether or not the token was known.
	w.WriteHeader(200)
}

func (s *server) introspect(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, 400, "invalid_request", "the body must be form-encoded")
		return
	}
	_, c, ok := s.authClient(r)
	if !ok || c.secret == "" {
		oauthError(w, 401, "invalid_client", "introspection needs a confidential client")
		return
	}
	claims, ok := s.verify(r.PostFormValue("token"))
	if !ok {
		labkit.JSON(w, 200, map[string]any{"active": false})
		return
	}
	claims["active"] = true
	labkit.JSON(w, 200, claims)
}

func (s *server) userinfo(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.verify(labkit.Bearer(r))
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		labkit.JSON(w, 401, map[string]string{"error": "invalid_token"})
		return
	}
	sub, _ := claims["sub"].(string)
	s.mu.Lock()
	u := s.userBySub(sub)
	s.mu.Unlock()
	if u == nil {
		labkit.JSON(w, 403, map[string]string{"error": "insufficient_scope", "error_description": "not a user token"})
		return
	}
	labkit.JSON(w, 200, map[string]any{"sub": u.sub, "email": u.email, "email_verified": true, "name": u.name})
}

var b64 = base64.RawURLEncoding

func (s *server) sign(claims map[string]any, ttl time.Duration) string {
	now := time.Now()
	claims["iat"], claims["exp"], claims["jti"] = now.Unix(), now.Add(ttl).Unix(), labkit.Token("")[:16]
	h, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": s.kid})
	c, _ := json.Marshal(claims)
	in := b64.EncodeToString(h) + "." + b64.EncodeToString(c)
	return in + "." + b64.EncodeToString(ed25519.Sign(s.key, []byte(in)))
}

func (s *server) verify(tok string) (map[string]any, bool) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, false
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(s.key.Public().(ed25519.PublicKey), []byte(parts[0]+"."+parts[1]), sig) {
		return nil, false
	}
	raw, err := b64.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return nil, false
	}
	if exp, _ := claims["exp"].(float64); time.Now().Unix() > int64(exp) {
		return nil, false
	}
	return claims, true
}
