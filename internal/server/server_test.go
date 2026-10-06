package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/keyring"
	"github.com/Ivan825/Stampede/internal/server"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

type client struct {
	t     *testing.T
	base  string
	http  *http.Client
	token string
	csrf  bool
}

func (c *client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+"/api/v1"+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if c.csrf {
		req.Header.Set("X-Stampede-CSRF", "1")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			c.t.Fatalf("%s %s: decode %q: %v", method, path, b, err)
		}
	}
	return resp.StatusCode
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar, Timeout: 30 * time.Second}, csrf: true}
}

func startServer(t *testing.T) string {
	t.Helper()
	st := storetest.Open(t)
	key, _ := keyringKey()
	srv, err := server.New(server.Config{Store: st, Keyring: key, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
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

func keyringKey() (*keyring.Keyring, error) {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return keyring.New(k)
}

type errBody struct {
	Error struct {
		Code    string   `json:"code"`
		Message string   `json:"message"`
		Details []string `json:"details"`
	} `json:"error"`
}

func setup(t *testing.T, c *client) {
	t.Helper()
	var v map[string]any
	if c.do("GET", "/version", nil, &v) != 200 || v["setupRequired"] != true {
		t.Fatalf("version: %v", v)
	}
	if code := c.do("POST", "/setup", map[string]string{"organisation": "Acme", "name": "Owner", "email": "owner@acme.test", "password": "correct horse battery"}, nil); code != 201 {
		t.Fatalf("setup: %d", code)
	}
}

func TestAuthFlow(t *testing.T) {
	base := startServer(t)
	c := newClient(t, base)
	setup(t, c)

	// Setup is one-time.
	if code := newClient(t, base).do("POST", "/setup", map[string]string{"organisation": "X", "name": "X", "email": "x@x.test", "password": "correct horse battery"}, nil); code != 409 {
		t.Errorf("second setup: %d", code)
	}
	var me map[string]any
	if c.do("GET", "/me", nil, &me) != 200 || me["role"] != "owner" {
		t.Fatalf("me: %v", me)
	}

	// Cookie auth without the CSRF header is refused for writes.
	c.csrf = false
	var e errBody
	if code := c.do("POST", "/projects", map[string]string{"name": "p"}, &e); code != 403 || e.Error.Code != "csrf" {
		t.Errorf("csrf: %d %+v", code, e)
	}
	c.csrf = true

	anon := newClient(t, base)
	if code := anon.do("GET", "/projects", nil, nil); code != 401 {
		t.Errorf("anonymous: %d", code)
	}
	for i := 0; i < 10; i++ {
		anon.do("POST", "/auth/login", map[string]string{"email": "owner@acme.test", "password": "wrong password!"}, nil)
	}
	if code := anon.do("POST", "/auth/login", map[string]string{"email": "owner@acme.test", "password": "correct horse battery"}, nil); code != 429 {
		t.Errorf("throttling: %d", code)
	}

	// A viewer cannot create projects; an API token cannot exceed its owner.
	if code := c.do("POST", "/users", map[string]string{"email": "v@acme.test", "name": "V", "role": "viewer", "password": "viewer password 1"}, nil); code != 201 {
		t.Fatalf("create viewer: %d", code)
	}
	v := newClient(t, base)
	if code := v.do("POST", "/auth/login", map[string]string{"email": "V@acme.test", "password": "viewer password 1"}, nil); code != 200 {
		t.Fatalf("viewer login: %d", code)
	}
	if code := v.do("POST", "/projects", map[string]string{"name": "nope"}, &e); code != 403 {
		t.Errorf("viewer create project: %d", code)
	}
	if code := v.do("POST", "/tokens", map[string]any{"name": "t", "role": "admin"}, nil); code != 403 {
		t.Errorf("token escalation: %d", code)
	}

	var tok map[string]any
	if code := c.do("POST", "/tokens", map[string]any{"name": "ci", "role": "runner"}, &tok); code != 201 {
		t.Fatalf("token: %d", code)
	}
	tc := &client{t: t, base: base, http: &http.Client{}, token: tok["secret"].(string)}
	if code := tc.do("GET", "/projects", nil, nil); code != 200 {
		t.Errorf("token read: %d", code)
	}
	if code := tc.do("POST", "/projects", map[string]string{"name": "x"}, nil); code != 403 {
		t.Errorf("runner token create project: %d", code)
	}
	if code := c.do("DELETE", "/tokens/"+tok["id"].(string), nil, nil); code != 204 {
		t.Errorf("revoke: %d", code)
	}
	if code := tc.do("GET", "/projects", nil, nil); code != 401 {
		t.Errorf("revoked token still works: %d", code)
	}

	// The last owner cannot be demoted.
	var users []map[string]any
	c.do("GET", "/users", nil, &users)
	var ownerID string
	for _, u := range users {
		if u["role"] == "owner" {
			ownerID = u["id"].(string)
		}
	}
	if code := c.do("PATCH", "/users/"+ownerID, map[string]string{"role": "admin"}, nil); code != 409 {
		t.Errorf("demote last owner: %d", code)
	}

	var audit []map[string]any
	if c.do("GET", "/audit", nil, &audit) != 200 || len(audit) < 4 {
		t.Errorf("audit entries: %d", len(audit))
	}
	if code := c.do("POST", "/auth/logout", nil, nil); code != 204 {
		t.Errorf("logout: %d", code)
	}
	if code := c.do("GET", "/me", nil, nil); code != 401 {
		t.Errorf("after logout: %d", code)
	}
}

func TestRunLifecycle(t *testing.T) {
	var hits int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("X-Key") != "s3cret" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `{"items":[{"id":3}]}`)
	}))
	defer target.Close()

	base := startServer(t)
	c := newClient(t, base)
	setup(t, c)

	var proj, tgt, sc map[string]any
	if code := c.do("POST", "/projects", map[string]string{"name": "Shop API"}, &proj); code != 201 {
		t.Fatalf("project: %d", code)
	}
	pid := proj["id"].(string)
	if proj["slug"] != "shop-api" {
		t.Errorf("slug %v", proj["slug"])
	}
	if code := c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "local", "baseURL": target.URL}, &tgt); code != 201 {
		t.Fatalf("target: %d", code)
	}
	if tgt["private"] != true || tgt["verified"] != true {
		t.Errorf("loopback target should be private and verified: %v", tgt)
	}
	if code := c.do("PUT", "/projects/"+pid+"/secrets", map[string]string{"name": "API_KEY", "value": "s3cret"}, nil); code != 200 {
		t.Fatalf("secret: %d", code)
	}
	var secrets []map[string]any
	c.do("GET", "/projects/"+pid+"/secrets", nil, &secrets)
	if len(secrets) != 1 || secrets[0]["value"] != nil {
		t.Errorf("secrets list must not include values: %v", secrets)
	}

	bad := "metadata: {name: x}\njourneys: []\nload: {vus: 1, duration: 1s}"
	var e errBody
	if code := c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": bad}, &e); code != 422 || len(e.Error.Details) == 0 {
		t.Errorf("invalid scenario: %d %+v", code, e)
	}
	yaml := `
metadata: {name: smoke, tags: [ci]}
target: {baseURL: "http://ignored.example"}
journeys:
  - name: list
    steps:
      - get: /items
        headers: {X-Key: "${secret.API_KEY}"}
        extract: {id: "$.items[0].id"}
      - get: /items/${id}
        headers: {X-Key: "${secret.API_KEY}"}
load: {mode: rate, rate: 40/s, duration: 3s}
targets: ["errors < 1%", "p95 < 1s"]`
	if code := c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": yaml}, &sc); code != 201 {
		t.Fatalf("scenario: %d", code)
	}
	sid := sc["id"].(string)
	var ver map[string]any
	if code := c.do("POST", "/scenarios/"+sid+"/versions", map[string]string{"yaml": yaml, "message": "same again"}, &ver); code != 201 || ver["version"].(float64) != 2 {
		t.Fatalf("version: %d %v", code, ver)
	}

	var run map[string]any
	if code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sid, "targetId": tgt["id"]}, &run); code != 201 {
		t.Fatalf("run: %d", code)
	}
	rid := run["id"].(string)

	// Follow the live stream to the end.
	req, _ := http.NewRequest("GET", base+"/api/v1/runs/"+rid+"/live", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("live: %d %s", resp.StatusCode, b)
	}
	points, statuses := 0, []string{}
	sc2 := bufio.NewScanner(resp.Body)
	var event string
	for sc2.Scan() {
		line := sc2.Text()
		if ev, ok := strings.CutPrefix(line, "event: "); ok {
			event = ev
			continue
		}
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		switch event {
		case "point":
			points++
		case "status":
			var st map[string]any
			_ = json.Unmarshal([]byte(data), &st)
			statuses = append(statuses, st["status"].(string))
		}
		if event == "end" {
			break
		}
	}
	resp.Body.Close()
	if points < 3 {
		t.Errorf("live points: %d", points)
	}
	if statuses[len(statuses)-1] != "completed" {
		t.Errorf("final status %v", statuses)
	}

	var got map[string]any
	c.do("GET", "/runs/"+rid, nil, &got)
	if got["status"] != "completed" || got["verdict"] != "pass" {
		t.Fatalf("run: %v", got)
	}
	summary := got["summary"].(map[string]any)
	if summary["requests"].(float64) != 240 {
		t.Errorf("40/s for 3s with 2 requests each should be 240 requests, got %v", summary["requests"])
	}
	var rep map[string]any
	if code := c.do("GET", "/runs/"+rid+"/report", nil, &rep); code != 200 || rep["verdict"] != "pass" {
		t.Errorf("report: %d %v", code, rep["verdict"])
	}
	var tl []map[string]any
	if c.do("GET", "/runs/"+rid+"/timeline", nil, &tl); len(tl) < 3 {
		t.Errorf("timeline %d", len(tl))
	}
	for _, f := range []string{"html", "junit", "markdown"} {
		r2, _ := http.NewRequest("GET", base+"/api/v1/runs/"+rid+"/report?format="+f, nil)
		resp, err := c.http.Do(r2)
		if err != nil || resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
			t.Errorf("format %s: %v %v", f, err, resp)
		}
		resp.Body.Close()
	}

	// Kill switch: a long run is killed and ends aborted.
	long := strings.Replace(yaml, "duration: 3s", "duration: 5m", 1)
	c.do("POST", "/scenarios/"+sid+"/versions", map[string]string{"yaml": long}, nil)
	c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sid, "targetId": tgt["id"]}, &run)
	rid = run["id"].(string)
	time.Sleep(1500 * time.Millisecond)
	var killed map[string][]string
	start := time.Now()
	if code := c.do("POST", "/runs/kill-all", nil, &killed); code != 200 || len(killed["killed"]) != 1 {
		t.Fatalf("kill-all: %d %v", code, killed)
	}
	for {
		c.do("GET", "/runs/"+rid, nil, &got)
		if got["status"] == "aborted" {
			break
		}
		if time.Since(start) > 10*time.Second {
			t.Fatalf("run not aborted: %v", got["status"])
		}
		time.Sleep(100 * time.Millisecond)
	}
	before := hits
	time.Sleep(500 * time.Millisecond)
	if hits != before {
		t.Errorf("load continued after kill: %d more requests", hits-before)
	}
}

func TestPublicTargetCaps(t *testing.T) {
	base := startServer(t)
	c := newClient(t, base)
	setup(t, c)
	var proj, tgt, sc map[string]any
	c.do("POST", "/projects", map[string]string{"name": "p"}, &proj)
	pid := proj["id"].(string)
	if code := c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "pub", "baseURL": "http://203.0.113.10"}, &tgt); code != 201 {
		t.Fatalf("target: %d", code)
	}
	if tgt["private"] != false || tgt["verified"] != false || tgt["verificationToken"] == "" {
		t.Fatalf("public target: %v", tgt)
	}
	c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": `
metadata: {name: big}
journeys: [{name: a, steps: [{get: /}]}]
load: {mode: rate, rate: 500/s, duration: 1m}`}, &sc)
	var e errBody
	code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &e)
	if code != 403 || !strings.Contains(e.Error.Message, "not verified") {
		t.Errorf("unverified public target should be capped: %d %+v", code, e)
	}
}
