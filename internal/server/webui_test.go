package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/server"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

// startServerWith starts a server whose config the caller adjusts.
func startServerWith(t *testing.T, adjust func(*server.Config)) string {
	t.Helper()
	st := storetest.Open(t)
	key, _ := keyringKey()
	cfg := server.Config{Store: st, Keyring: key, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	adjust(&cfg)
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		hs.Close()
	})
	return hs.URL
}

// viewerClient adds a viewer to the organisation and signs in as them.
func viewerClient(t *testing.T, base string, owner *client) *client {
	t.Helper()
	if code := owner.do("POST", "/users", map[string]string{"email": "v@acme.test", "name": "V", "role": "viewer", "password": "viewer password 1"}, nil); code != 201 {
		t.Fatalf("add viewer: %d", code)
	}
	v := newClient(t, base)
	if code := v.do("POST", "/auth/login", map[string]string{"email": "v@acme.test", "password": "viewer password 1"}, nil); code != 200 {
		t.Fatalf("viewer login: %d", code)
	}
	return v
}

func TestRunWorkersInProcess(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	base := startServer(t)
	c := newClient(t, base)
	setup(t, c)
	var proj, tgt, sc, run map[string]any
	c.do("POST", "/projects", map[string]string{"name": "p"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "t", "baseURL": target.URL}, &tgt)
	c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": `
metadata: {name: local}
journeys: [{name: a, steps: [{get: /}]}]
load: {mode: rate, rate: 20/s, duration: 4s}`}, &sc)
	if code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &run); code != 201 {
		t.Fatalf("run %d", code)
	}
	rid := run["id"].(string)
	type health struct {
		Live    bool `json:"live"`
		Workers []struct {
			ID              string   `json:"id"`
			Name            string   `json:"name"`
			Status          string   `json:"status"`
			Saturated       bool     `json:"saturated"`
			SchedLagP99     *float64 `json:"schedLagP99"`
			LastHeartbeatAt *string  `json:"lastHeartbeatAt"`
		} `json:"workers"`
	}
	seen := false
	for start := time.Now(); time.Since(start) < 20*time.Second && !seen; time.Sleep(100 * time.Millisecond) {
		var h health
		if code := c.do("GET", "/runs/"+rid+"/workers", nil, &h); code != 200 {
			t.Fatalf("run workers: %d", code)
		}
		for _, w := range h.Workers {
			if h.Live && w.ID == "local" && w.Name == "this server" && w.LastHeartbeatAt != nil && w.SchedLagP99 != nil {
				seen = w.Status == "running" || w.Status == "saturated"
			}
		}
	}
	if !seen {
		t.Fatal("an in-process run should report the server's own health while it runs")
	}

	if code := c.do("GET", "/runs/00000000-0000-0000-0000-000000000000/workers", nil, nil); code != 404 {
		t.Errorf("unknown run: %d", code)
	}
}

func TestPacksAPI(t *testing.T) {
	base := startServer(t)
	c := newClient(t, base)
	setup(t, c)
	var list []struct {
		Name, Title, Status, Signature string
		Drivers                        []string
	}
	if code := c.do("GET", "/packs", nil, &list); code != 200 || len(list) < 10 {
		t.Fatalf("packs: %d %v", code, list)
	}
	found := false
	for _, p := range list {
		if p.Name == "ecommerce" {
			found = p.Status == "shipped" && len(p.Drivers) > 0 && p.Signature != ""
		}
	}
	if !found {
		t.Errorf("catalogue should list the shipped ecommerce pack: %v", list)
	}

	var p struct {
		Name      string
		Protocols []string
		Variables []struct{ Name, Description string }
		Files     []struct {
			Path, Kind, Scenario, Description, Yaml string
			Journeys                                []string
			Shape                                   *string
		}
	}
	if code := c.do("GET", "/packs/ecommerce", nil, &p); code != 200 {
		t.Fatalf("pack: %d", code)
	}
	if p.Name != "ecommerce" || len(p.Variables) == 0 || p.Variables[0].Name != "TARGET_URL" {
		t.Errorf("pack: %+v", p)
	}
	kinds := map[string]bool{}
	for _, f := range p.Files {
		kinds[f.Kind] = true
		if f.Path == "journeys/failed-login.yaml" {
			if f.Scenario != "ecommerce-failed-login" || len(f.Journeys) != 1 || f.Journeys[0] != "wrong-password" {
				t.Errorf("failed-login: %+v", f)
			}
			if !strings.HasPrefix(f.Description, "Edge journey: wrong passwords") {
				t.Errorf("description from the opening comment: %q", f.Description)
			}
			if !strings.Contains(f.Yaml, "kind: Scenario") {
				t.Error("yaml missing")
			}
		}
	}
	if !kinds["journey"] || !kinds["stress"] {
		t.Errorf("files should include journeys and stresses: %v", kinds)
	}
	for _, name := range []string{"nope", "..", "ecommerce%2F..%2F.."} {
		if code := c.do("GET", "/packs/"+name, nil, nil); code != 404 {
			t.Errorf("GET /packs/%s: %d", name, code)
		}
	}
}

const shopSpec = `openapi: 3.0.3
info: {title: shop, version: "2"}
paths:
  /api/products:
    get: {summary: List products, responses: {"200": {description: ok}}}
  /api/products/{id}:
    get: {responses: {"200": {description: ok}}}
  /api/cart:
    post: {responses: {"200": {description: ok}}}
`

const shopSpecV1 = shopSpec + `  /api/wishlist:
    get: {responses: {"200": {description: ok}}}
`

func TestScenarioCoverageAndDrift(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.yaml":
			io.WriteString(w, shopSpec) //nolint:errcheck
		case "/api/products", "/api/products/1":
			io.WriteString(w, `{"items":[{"id":1}]}`) //nolint:errcheck
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()
	base := startServer(t)
	c := newClient(t, base)
	setup(t, c)
	var proj, tgt, sc map[string]any
	c.do("POST", "/projects", map[string]string{"name": "shop"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "t", "baseURL": target.URL}, &tgt)
	if code := c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": `
metadata: {name: shop}
journeys:
  - name: browse
    steps:
      - get: /api/products
        check: {status: 200}
      - get: /api/products/1
  - name: wish
    steps:
      - get: /api/wishlist
        check: {status: 200}
load: {vus: 1, duration: 5s}`}, &sc); code != 201 {
		t.Fatalf("scenario: %d", code)
	}
	sid := sc["id"].(string)

	type coverage struct {
		Version   int
		Covered   int
		Total     int
		Endpoints []struct {
			Method, Path string
			Summary      *string
			Journeys     []string
		}
		Unmatched []struct{ Journey, Method, URL string }
	}
	var cov coverage
	if code := c.do("POST", "/scenarios/"+sid+"/coverage", map[string]any{"openapi": shopSpec}, &cov); code != 200 {
		t.Fatalf("coverage: %d", code)
	}
	if cov.Version != 1 || cov.Covered != 2 || cov.Total != 3 {
		t.Errorf("coverage: %+v", cov)
	}
	if len(cov.Unmatched) != 1 || cov.Unmatched[0].Journey != "wish" || cov.Unmatched[0].URL != "/api/wishlist" {
		t.Errorf("unmatched: %+v", cov.Unmatched)
	}
	for _, e := range cov.Endpoints {
		if e.Path == "/api/products" && (len(e.Journeys) != 1 || e.Journeys[0] != "browse" || e.Summary == nil) {
			t.Errorf("endpoint: %+v", e)
		}
		if e.Path == "/api/cart" && len(e.Journeys) != 0 {
			t.Errorf("cart is not covered: %+v", e)
		}
	}

	// By URL from the target's host.
	var byURL coverage
	if code := c.do("POST", "/scenarios/"+sid+"/coverage", map[string]any{"specURL": target.URL + "/openapi.yaml"}, &byURL); code != 200 || byURL.Total != 3 {
		t.Errorf("coverage by URL: %d %+v", code, byURL)
	}
	var e errBody
	if code := c.do("POST", "/scenarios/"+sid+"/coverage", map[string]any{"specURL": "http://elsewhere.example/openapi.yaml"}, &e); code != 422 || !strings.Contains(e.Error.Message, "not the host of a target") {
		t.Errorf("other host: %d %+v", code, e)
	}
	if code := c.do("POST", "/scenarios/"+sid+"/coverage", map[string]any{}, &e); code != 422 {
		t.Errorf("no spec: %d", code)
	}
	if code := c.do("POST", "/scenarios/"+sid+"/coverage", map[string]any{"openapi": "openapi: 3.0.3\ninfo: {title: x, version: '1'}\npaths: {}\n"}, &e); code != 422 {
		t.Errorf("spec without endpoints: %d", code)
	}

	// A viewer may check coverage of a pasted document but not have the
	// server fetch one or send requests.
	v := viewerClient(t, base, c)
	if code := v.do("POST", "/scenarios/"+sid+"/coverage", map[string]any{"openapi": shopSpec}, nil); code != 200 {
		t.Errorf("viewer coverage: %d", code)
	}
	if code := v.do("POST", "/scenarios/"+sid+"/coverage", map[string]any{"specURL": target.URL + "/openapi.yaml"}, nil); code != 403 {
		t.Errorf("viewer fetch by URL: %d", code)
	}
	if code := v.do("POST", "/scenarios/"+sid+"/drift", map[string]any{"openapi": shopSpec, "targetId": tgt["id"]}, nil); code != 403 {
		t.Errorf("viewer dry run: %d", code)
	}

	// Drift: /api/wishlist was removed between v1 and v2 of the API.
	var d struct {
		Drifted bool
		Removed []struct{ Method, Path string }
		Added   []struct{ Method, Path string }
		Broken  []struct {
			Journey   string
			Endpoints []struct{ Method, Path string }
		}
		Unmatched []struct{ Journey, URL string }
		DryRun    []struct {
			Journey string
			OK      bool `json:"ok"`
			Step    *string
			Status  *int
		}
	}
	if code := c.do("POST", "/scenarios/"+sid+"/drift", map[string]any{
		"openapi": shopSpec, "previousOpenapi": shopSpecV1, "targetId": tgt["id"],
	}, &d); code != 200 {
		t.Fatalf("drift: %d", code)
	}
	if !d.Drifted || len(d.Removed) != 1 || d.Removed[0].Path != "/api/wishlist" || len(d.Added) != 0 {
		t.Errorf("drift diff: %+v", d)
	}
	if len(d.Broken) != 1 || d.Broken[0].Journey != "wish" {
		t.Errorf("broken: %+v", d.Broken)
	}
	if len(d.Unmatched) != 1 {
		t.Errorf("unmatched: %+v", d.Unmatched)
	}
	ok := map[string]bool{}
	for _, j := range d.DryRun {
		ok[j.Journey] = j.OK
		if j.Journey == "wish" && (j.Status == nil || *j.Status != 404) {
			t.Errorf("wish should fail with 404: %+v", j)
		}
	}
	if !ok["browse"] || ok["wish"] || len(d.DryRun) != 2 {
		t.Errorf("dry run: %+v", d.DryRun)
	}

	// Without a previous version or a target only unmatched requests count.
	var plain map[string]any
	if code := c.do("POST", "/scenarios/"+sid+"/drift", map[string]any{"openapi": shopSpecV1}, &plain); code != 200 || plain["drifted"] != false || plain["dryRun"] != nil {
		t.Errorf("drift against v1: %d %v", code, plain)
	}
}

func TestSettingsAPI(t *testing.T) {
	floor := scenario.Percent(0.9)
	base := startServerWith(t, func(cfg *server.Config) {
		cfg.HardCaps = safety.Caps{MaxRate: 5000, MaxVUs: 2000, MaxDuration: 2 * time.Hour}
		cfg.AbortFloor = &scenario.Abort{Errors: &floor, For: scenario.Duration(30 * time.Second)}
		cfg.OIDC = &server.OIDCConfig{
			Issuer: "https://idp.example.com", ClientID: "stampede-client", ClientSecret: "very-secret-value",
			RedirectURL: "https://stampede.example.com/api/v1/auth/oidc/callback", Name: "Okta",
			AllowedDomains: []string{"Acme.test"}, DefaultRole: auth.RoleViewer,
		}
	})
	c := newClient(t, base)
	setup(t, c)

	resp, err := c.http.Get(base + "/api/v1/settings/sso")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("sso: %d", resp.StatusCode)
	}
	for _, secret := range []string{"very-secret-value", "stampede-client"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("SSO settings must not include %q: %s", secret, raw)
		}
	}
	var sso map[string]any
	c.do("GET", "/settings/sso", nil, &sso)
	if sso["enabled"] != true || sso["issuer"] != "https://idp.example.com" || sso["defaultRole"] != "viewer" || sso["name"] != "Okta" {
		t.Errorf("sso: %v", sso)
	}
	if d := sso["allowedDomains"].([]any); len(d) != 1 || d[0] != "acme.test" {
		t.Errorf("domains: %v", d)
	}
	if s := sso["scopes"].([]any); len(s) != 3 || s[0] != "openid" {
		t.Errorf("scopes: %v", s)
	}

	var proj, verified, public map[string]any
	c.do("POST", "/projects", map[string]string{"name": "p"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "local", "baseURL": "http://127.0.0.1:9", "caps": map[string]any{"maxRate": 100}}, &verified)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "public", "baseURL": "https://example.com"}, &public)

	var lim struct {
		Server           struct{ MaxRate, MaxVUs, MaxDurationSeconds *float64 }
		UnverifiedPublic struct{ MaxRate, MaxVUs, MaxDurationSeconds *float64 }
		AbortFloor       *struct{ ErrorRate, ForSeconds float64 }
		Targets          []struct {
			Name, ProjectName string
			Verified          bool
			Caps              struct{ MaxRate, MaxVUs *float64 }
			Effective         struct{ MaxRate, MaxVUs, MaxDurationSeconds *float64 }
		}
	}
	if code := c.do("GET", "/settings/limits", nil, &lim); code != 200 {
		t.Fatalf("limits: %d", code)
	}
	if *lim.Server.MaxRate != 5000 || *lim.Server.MaxVUs != 2000 || *lim.Server.MaxDurationSeconds != 7200 {
		t.Errorf("server caps: %+v", lim.Server)
	}
	if *lim.UnverifiedPublic.MaxRate != 50 {
		t.Errorf("unverified caps: %+v", lim.UnverifiedPublic)
	}
	if lim.AbortFloor == nil || lim.AbortFloor.ErrorRate != 0.9 || lim.AbortFloor.ForSeconds != 30 {
		t.Errorf("abort floor: %+v", lim.AbortFloor)
	}
	if len(lim.Targets) != 2 {
		t.Fatalf("targets: %+v", lim.Targets)
	}
	for _, tg := range lim.Targets {
		switch tg.Name {
		case "local":
			if !tg.Verified || *tg.Caps.MaxRate != 100 || *tg.Effective.MaxRate != 100 || *tg.Effective.MaxVUs != 2000 || tg.ProjectName != "p" {
				t.Errorf("local: %+v", tg)
			}
		case "public":
			if tg.Verified || tg.Caps.MaxRate != nil || *tg.Effective.MaxRate != 50 || *tg.Effective.MaxDurationSeconds != 600 {
				t.Errorf("public: %+v", tg)
			}
		}
	}

	v := viewerClient(t, base, c)
	for _, p := range []string{"/settings/sso", "/settings/limits"} {
		if code := v.do("GET", p, nil, nil); code != 403 {
			t.Errorf("viewer %s: %d", p, code)
		}
	}

	// Without OIDC, SSO is reported off and password sign-in on.
	plain := newClient(t, startServer(t))
	setup(t, plain)
	var off map[string]any
	if plain.do("GET", "/settings/sso", nil, &off); off["enabled"] != false || off["passwordLogin"] != true || off["issuer"] != nil {
		t.Errorf("sso off: %v", off)
	}
}
