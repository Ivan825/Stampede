package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/ai/provider"
	"github.com/Ivan825/Stampede/internal/server"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

// aiFakes hands the server whichever fake provider the test set last and
// records the configuration it was built with.
type aiFakes struct {
	mu   sync.Mutex
	next *provider.Fake
	cfgs []provider.Config
}

func (f *aiFakes) set(p *provider.Fake) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next = p
}

func (f *aiFakes) build(cfg provider.Config) (provider.Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfgs = append(f.cfgs, cfg)
	return f.next, nil
}

func (f *aiFakes) lastKey() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cfgs[len(f.cfgs)-1].APIKey
}

func startAIServer(t *testing.T, fakes *aiFakes) (string, *store.Store, *server.Server) {
	t.Helper()
	st := storetest.Open(t)
	key, _ := keyringKey()
	srv, err := server.New(server.Config{
		Store: st, Keyring: key, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		AI: server.AIConfig{NewProvider: fakes.build, Workers: 1},
	})
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
	return hs.URL, st, srv
}

// aiShop is the dry-run target: login, then an authenticated cart.
func aiShop(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/items", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"id":5,"name":"Lamp"}]}`))
	})
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"token":"tok-0123456789abcdef"}`))
	})
	mux.HandleFunc("POST /api/cart", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-0123456789abcdef" {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"ok":true,"owner":"pat@example.org"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

const aiSpec = `openapi: 3.0.3
info: { title: Items, version: "1" }
paths:
  /api/items:
    get: { responses: { "200": { description: ok } } }
  /api/login:
    post: { responses: { "200": { description: ok } } }
  /api/cart:
    post: { responses: { "201": { description: ok } } }
`

// The first draft uses a variable it never extracts; the repair is valid.
const (
	aiDraftBad = `{"metadata":{"name":"items-mix"},"journeys":[{"name":"buy","steps":[
	  {"post":"/api/cart","headers":{"Authorization":"Bearer ${token}"},"json":{"itemId":5},"check":{"status":201}}]}],
	  "load":{"mode":"vus","vus":2,"duration":"30s"}}`
	aiDraftGood = `{"metadata":{"name":"items-mix"},"journeys":[
	  {"name":"browse","weight":4,"steps":[{"get":"/api/items","check":{"status":200}}]},
	  {"name":"buy","weight":1,"steps":[
	    {"post":"/api/login","json":{"email":"u1@shop.test","password":"pw"},"check":{"status":200},"extract":{"token":"$.token"}},
	    {"think":"1s"},
	    {"post":"/api/cart","headers":{"Authorization":"Bearer ${token}"},"json":{"itemId":5},"check":{"status":201}}]}],
	  "load":{"mode":"vus","vus":2,"duration":"30s"}}`
	// Expects 200 from the cart, which answers 201.
	aiDraftFlagged = `{"metadata":{"name":"items-mix"},"journeys":[
	  {"name":"browse","steps":[{"get":"/api/items","check":{"status":200}}]},
	  {"name":"buy","steps":[
	    {"post":"/api/login","json":{"email":"u1@shop.test","password":"pw"},"extract":{"token":"$.token"}},
	    {"post":"/api/cart","headers":{"Authorization":"Bearer ${token}"},"json":{"itemId":5},"check":{"status":200}}]}],
	  "load":{"mode":"vus","vus":2,"duration":"30s"}}`
)

type aiJob struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Stage    string `json:"stage"`
	Error    string `json:"error"`
	Yaml     string `json:"yaml"`
	Diff     string `json:"diff"`
	Usage    struct{ InputTokens, OutputTokens int64 }
	Journeys []struct {
		Name     string `json:"name"`
		Status   string `json:"status"`
		Attempts int    `json:"attempts"`
		Traces   []struct {
			OK    bool `json:"ok"`
			Steps []struct {
				Status          int               `json:"status"`
				RequestHeaders  map[string]string `json:"requestHeaders"`
				ResponseBody    string            `json:"responseBody"`
				Extracted       map[string]string `json:"extracted"`
				OK              bool              `json:"ok"`
				ResponseHeaders map[string]string `json:"responseHeaders"`
			} `json:"steps"`
		} `json:"traces"`
	} `json:"journeys"`
	ApprovedVersion *int `json:"approvedVersion"`
}

func waitJob(t *testing.T, c *client, id string) aiJob {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var j aiJob
		if code := c.do("GET", "/ai/jobs/"+id, nil, &j); code != 200 {
			t.Fatalf("get job: %d", code)
		}
		if j.Status != "queued" && j.Status != "running" {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job still %s at stage %s", j.Status, j.Stage)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAIJourneyGeneration(t *testing.T) {
	fakes := &aiFakes{}
	base, st, _ := startAIServer(t, fakes)
	c := newClient(t, base)
	setup(t, c)
	shop := aiShop(t)

	var proj, tgt map[string]any
	if code := c.do("POST", "/projects", map[string]string{"name": "Shop"}, &proj); code != 201 {
		t.Fatalf("project: %d", code)
	}
	pid := proj["id"].(string)
	if code := c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "local", "baseURL": shop.URL}, &tgt); code != 201 {
		t.Fatalf("target: %d", code)
	}
	tid := tgt["id"].(string)
	jobBody := map[string]any{"description": "people browse and buy lamps", "openapi": aiSpec, "targetId": tid}

	// No provider yet.
	var e errBody
	if code := c.do("POST", "/projects/"+pid+"/ai/jobs", jobBody, &e); code != 409 || !strings.Contains(e.Error.Message, "no AI provider") {
		t.Fatalf("job without provider: %d %+v", code, e)
	}

	// Providers: the key is required, encrypted and never returned.
	const apiKey = "sk-ant-test-0123456789-SECRET"
	if code := c.do("POST", "/ai/providers", map[string]any{"kind": "anthropic"}, &e); code != 422 {
		t.Errorf("anthropic without a key: %d", code)
	}
	var prov map[string]any
	if code := c.do("POST", "/ai/providers", map[string]any{"kind": "anthropic", "apiKey": apiKey}, &prov); code != 201 {
		t.Fatalf("create provider: %d", code)
	}
	if prov["model"] != "claude-sonnet-5-5" || prov["hasKey"] != true || prov["name"] != "default" {
		t.Errorf("provider: %v", prov)
	}
	if code := c.do("POST", "/ai/providers", map[string]any{"kind": "anthropic", "model": "claude-opus-5-5", "monthlyTokenCap": 1_000_000}, &prov); code != 200 || prov["hasKey"] != true {
		t.Fatalf("replace provider keeps the key: %d %v", code, prov)
	}
	var list []map[string]any
	c.do("GET", "/ai/providers", nil, &list)
	raw, _ := json.Marshal(list)
	if len(list) != 1 || strings.Contains(string(raw), apiKey) {
		t.Errorf("provider list must never contain the key: %s", raw)
	}

	// A viewer cannot configure providers or start jobs.
	if code := c.do("POST", "/users", map[string]string{"email": "v@acme.test", "name": "V", "role": "viewer", "password": "viewer password 1"}, nil); code != 201 {
		t.Fatalf("create viewer: %d", code)
	}
	v := newClient(t, base)
	v.do("POST", "/auth/login", map[string]string{"email": "v@acme.test", "password": "viewer password 1"}, nil)
	if code := v.do("POST", "/ai/providers", map[string]any{"kind": "ollama", "model": "llama"}, nil); code != 403 {
		t.Errorf("viewer configures provider: %d", code)
	}
	if code := v.do("POST", "/projects/"+pid+"/ai/jobs", jobBody, nil); code != 403 {
		t.Errorf("viewer starts job: %d", code)
	}

	// A job that repairs a static problem, then passes its dry run.
	fake := &provider.Fake{Replies: []any{aiDraftBad, aiDraftGood}, PerCall: provider.Usage{InputTokens: 900, OutputTokens: 100}}
	fakes.set(fake)
	var job aiJob
	if code := c.do("POST", "/projects/"+pid+"/ai/jobs", jobBody, &job); code != 202 {
		t.Fatalf("create job: %d", code)
	}
	if fakes.lastKey() != apiKey {
		t.Error("the provider must be built with the decrypted key")
	}
	job = waitJob(t, c, job.ID)
	if job.Status != "succeeded" || job.Stage != "done" || job.Usage.InputTokens != 1800 || job.Usage.OutputTokens != 200 {
		t.Fatalf("job: %+v", job)
	}
	if len(job.Journeys) != 2 || job.Journeys[1].Name != "buy" || job.Journeys[1].Status != "passed" {
		t.Fatalf("journeys: %+v", job.Journeys)
	}
	steps := job.Journeys[1].Traces[0].Steps
	if len(steps) != 2 || steps[1].Status != 201 || steps[1].RequestHeaders["Authorization"] != "[REDACTED]" ||
		steps[0].Extracted["token"] != "[REDACTED]" || strings.Contains(steps[1].ResponseBody, "pat@example.org") {
		t.Errorf("trace must be recorded and redacted: %+v", steps)
	}
	if strings.Contains(job.Yaml, "baseURL") || !strings.Contains(job.Yaml, "name: items-mix") {
		t.Errorf("proposal: %s", job.Yaml)
	}
	if !strings.Contains(fake.Requests()[1].Messages[2].Content, "token") {
		t.Error("the static problem should go back to the model")
	}
	var jobs []map[string]any
	if c.do("GET", "/projects/"+pid+"/ai/jobs", nil, &jobs) != 200 || len(jobs) != 1 {
		t.Errorf("list jobs: %v", jobs)
	}

	// Approval saves a new scenario; it happens once and needs an editor.
	if code := v.do("POST", "/ai/jobs/"+job.ID+"/approve", map[string]any{}, nil); code != 403 {
		t.Errorf("viewer approves: %d", code)
	}
	var appr struct {
		Scenario struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"scenario"`
		Version int `json:"version"`
	}
	if code := c.do("POST", "/ai/jobs/"+job.ID+"/approve", map[string]any{}, &appr); code != 201 || appr.Version != 1 || appr.Scenario.Name != "items-mix" {
		t.Fatalf("approve: %d %+v", code, appr)
	}
	if code := c.do("POST", "/ai/jobs/"+job.ID+"/approve", map[string]any{}, &e); code != 409 {
		t.Errorf("second approval: %d", code)
	}

	// A job compared with that scenario, whose cart journey never passes.
	fakes.set(&provider.Fake{Replies: []any{aiDraftFlagged, aiDraftFlagged}, PerCall: provider.Usage{InputTokens: 10, OutputTokens: 10}})
	body2 := map[string]any{"openapi": aiSpec, "targetId": tid, "scenarioId": appr.Scenario.ID, "maxRepairs": 1}
	if code := c.do("POST", "/projects/"+pid+"/ai/jobs", body2, &job); code != 202 {
		t.Fatalf("create job 2: %d", code)
	}
	job = waitJob(t, c, job.ID)
	if job.Status != "needs_review" || job.Diff == "" || !strings.Contains(job.Yaml, "FLAGGED FOR REVIEW") {
		t.Fatalf("job 2: %+v", job)
	}
	if code := c.do("POST", "/ai/jobs/"+job.ID+"/approve", map[string]any{}, &e); code != 409 || !strings.Contains(e.Error.Message, "buy") {
		t.Errorf("approving flagged journeys needs allowUnvalidated: %d %+v", code, e)
	}
	if code := c.do("POST", "/ai/jobs/"+job.ID+"/approve", map[string]any{"allowUnvalidated": true, "message": "reviewed by hand"}, &appr); code != 201 || appr.Version != 2 {
		t.Fatalf("approve flagged: %d %+v", code, appr)
	}
	var versions []map[string]any
	c.do("GET", "/scenarios/"+appr.Scenario.ID+"/versions", nil, &versions)
	if len(versions) != 2 || versions[0]["message"] != "reviewed by hand" {
		t.Errorf("versions: %v", versions)
	}

	// The audit log records provider changes and approvals, never the key.
	var audit []map[string]any
	c.do("GET", "/audit", nil, &audit)
	ab, _ := json.Marshal(audit)
	for _, want := range []string{"ai.provider.put", "ai.job.create", "ai.job.approve"} {
		if !strings.Contains(string(ab), want) {
			t.Errorf("audit lacks %s", want)
		}
	}
	if strings.Contains(string(ab), apiKey) {
		t.Error("audit log contains the key")
	}

	// The monthly cap refuses new jobs once reached.
	if code := c.do("POST", "/ai/providers", map[string]any{"kind": "anthropic", "monthlyTokenCap": 1000}, nil); code != 200 {
		t.Fatalf("lower cap: %d", code)
	}
	if code := c.do("POST", "/projects/"+pid+"/ai/jobs", jobBody, &e); code != 429 || e.Error.Code != "ai_token_cap" {
		t.Errorf("over the cap: %d %+v", code, e)
	}
	c.do("GET", "/ai/providers", nil, &list)
	if list[0]["usedTokensThisMonth"].(float64) != 2040 {
		t.Errorf("usage this month: %v", list[0]["usedTokensThisMonth"])
	}

	// Jobs left unfinished by a previous process are marked failed.
	var org uuid.UUID
	var me map[string]any
	c.do("GET", "/me", nil, &me)
	org = uuid.MustParse(me["orgId"].(string))
	stale := uuid.New()
	if err := st.CreateAIJob(context.Background(), db.CreateAIJobParams{
		ID: stale, OrgID: org, ProjectID: uuid.MustParse(pid), ProviderKind: "anthropic", Model: "m", DryRun: false, Inputs: []byte("{}"),
	}); err != nil {
		t.Fatal(err)
	}
	srv2, err := server.New(server.Config{Store: st, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv2.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	var j3 aiJob
	c.do("GET", "/ai/jobs/"+stale.String(), nil, &j3)
	if j3.Status != "failed" || !strings.Contains(j3.Error, "interrupted") {
		t.Errorf("interrupted job: %+v", j3)
	}

	// Deleting the provider leaves past jobs readable.
	if code := c.do("DELETE", "/ai/providers/"+list[0]["id"].(string), nil, nil); code != 204 {
		t.Errorf("delete provider: %d", code)
	}
	if code := c.do("GET", "/ai/jobs/"+job.ID, nil, nil); code != 200 {
		t.Errorf("job after provider delete: %d", code)
	}
}
