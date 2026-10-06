package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/ai/provider"
	"github.com/Ivan825/Stampede/internal/scenario"
)

const shopToken = "tok-abcdef1234567890"

// shopServer is a tiny API with login, an authenticated profile and
// checkout, used as the dry-run target.
func shopServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	authed := func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+shopToken }
	mux.HandleFunc("GET /api/products", func(w http.ResponseWriter, _ *http.Request) {
		write(w, 200, map[string]any{"products": []any{map[string]any{"id": 7, "name": "Yo-yo"}}})
	})
	mux.HandleFunc("GET /api/products/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "7" {
			write(w, 404, map[string]any{"error": "no such product"})
			return
		}
		write(w, 200, map[string]any{"id": 7, "name": "Yo-yo"})
	})
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Email, Password string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Password != "shop-pass" {
			write(w, 401, map[string]any{"error": "invalid email or password"})
			return
		}
		write(w, 200, map[string]any{"token": shopToken, "tokenType": "Bearer"})
	})
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			write(w, 401, map[string]any{"error": "sign in"})
			return
		}
		write(w, 200, map[string]any{"id": 1, "email": "real.person@example.com", "phone": "+1 415 555 0199"})
	})
	mux.HandleFunc("POST /api/checkout", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			write(w, 401, map[string]any{"error": "sign in"})
			return
		}
		write(w, 201, map[string]any{"orderId": 99, "receipt": "sent to real.person@example.com"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

const shopSpec = `openapi: 3.0.3
info:
  title: Shop
  version: "1"
  description: Test user user1@shop.test, password shop-pass.
paths:
  /api/products:
    get:
      summary: List products
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  products:
                    type: array
                    items:
                      type: object
                      properties:
                        id: { type: integer }
                        name: { type: string }
  /api/products/{id}:
    get:
      parameters:
        - { name: id, in: path, required: true, schema: { type: integer } }
      responses:
        "200": { description: ok }
  /api/login:
    post:
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                email: { type: string }
                password: { type: string }
            example: { email: user1@shop.test, password: shop-pass }
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  token: { type: string }
        "401": { description: wrong password }
  /api/me:
    get:
      security: [{ bearer: [] }]
      responses:
        "200": { description: ok }
  /api/checkout:
    post:
      security: [{ bearer: [] }]
      responses:
        "201":
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  orderId: { type: integer }
components:
  securitySchemes:
    bearer: { type: http, scheme: bearer }
`

func toJSON(t *testing.T, yamlSrc string) string {
	t.Helper()
	s, err := scenario.Decode([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("test scenario does not decode: %v", err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const browseJourney = `
  - name: browse
    weight: 8
    steps:
      - get: /api/products
        check: { status: 200 }
        extract: { productId: "$.products[0].id" }
      - think: 1s..2s
      - get: /api/products/${productId}
        check: { status: 200 }`

// draft 1: buy uses ${token} without extracting it and calls an endpoint
// that does not exist.
var draft1 = `
apiVersion: stampede.dev/v1
kind: Scenario
metadata: { name: shop-mix }
journeys:` + browseJourney + `
  - name: buy
    weight: 2
    steps:
      - post: /api/basket
        json: { productId: 7 }
      - post: /api/checkout
        headers: { Authorization: "Bearer ${token}" }
        check: { status: 201 }
load: { mode: vus, vus: 5, duration: 1m }
`

// draft 2: compiles, but checkout expects 200 (the API returns 201).
var draft2 = `
apiVersion: stampede.dev/v1
kind: Scenario
metadata: { name: shop-mix }
journeys:` + browseJourney + `
  - name: buy
    weight: 2
    steps:
      - post: /api/login
        json: { email: user1@shop.test, password: shop-pass }
        extract: { token: "$.token" }
      - get: /api/me
        headers: { Authorization: "Bearer ${token}" }
        extract: { email: "$.email" }
      - post: /api/checkout
        headers: { Authorization: "Bearer ${token}" }
        check: { status: 200 }
  - name: failed-login
    weight: 1
    steps:
      - post: /api/login
        json: { email: user1@shop.test, password: wrong-pass }
        check: { status: 401 }
load: { mode: vus, vus: 5, duration: 1m }
`

var draft3 = strings.Replace(draft2, "check: { status: 200 }\n  - name: failed-login", "check: { status: 201 }\n  - name: failed-login", 1)

func TestPipelineRepairsUntilTheDryRunPasses(t *testing.T) {
	srv := shopServer(t)
	fake := &provider.Fake{
		Replies: []any{"```json\n" + toJSON(t, draft1) + "\n```", toJSON(t, draft2), toJSON(t, draft3)},
		PerCall: provider.Usage{InputTokens: 1000, OutputTokens: 200},
	}
	var stages []string
	res, err := Generate(context.Background(), Inputs{
		Description: "Shoppers browse; some log in and buy.",
		OpenAPI:     []byte(shopSpec),
		Existing:    []byte("apiVersion: stampede.dev/v1\nkind: Scenario\nmetadata: {name: old}\n"),
	}, Options{
		Provider: fake, Target: srv.URL, DryRun: true,
		Progress: func(p Progress) { stages = append(stages, p.Stage) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Validated() || res.Fatal() {
		t.Fatalf("not validated: problems=%v journeys=%+v", res.Problems, res.Journeys)
	}
	if res.Rounds != 2 || len(fake.Requests()) != 3 {
		t.Errorf("rounds=%d calls=%d", res.Rounds, len(fake.Requests()))
	}
	if res.Usage.Total() != 3600 {
		t.Errorf("usage %+v", res.Usage)
	}
	if len(res.Journeys) != 3 {
		t.Fatalf("journeys: %+v", res.Journeys)
	}
	for _, j := range res.Journeys {
		if j.Status != JourneyPassed || len(j.Traces) == 0 || !j.Traces[0].OK {
			t.Errorf("journey %s: %+v", j.Name, j)
		}
		// Round 0 did not compile, so journeys were dry-run in rounds 1 and 2.
		if j.Name == "browse" && j.Attempts != 2 {
			t.Errorf("browse attempts = %d", j.Attempts)
		}
	}
	buy := res.Journeys[1]
	if buy.Name != "buy" || len(buy.Traces[0].Steps) != 3 || buy.Traces[0].Steps[0].Extracted["token"] != redacted {
		t.Errorf("buy trace: %+v", buy.Traces)
	}
	if got := buy.Traces[0].Steps[1].RequestHeaders["Authorization"]; got != redacted {
		t.Errorf("Authorization recorded as %q", got)
	}

	// The first repair carries the static problems.
	reqs := fake.Requests()
	repair1 := reqs[1].Messages[2].Content
	for _, want := range []string{"token", "POST /api/basket is not an endpoint"} {
		if !strings.Contains(repair1, want) {
			t.Errorf("repair 1 lacks %q:\n%s", want, repair1)
		}
	}
	// The second carries dry-run evidence, redacted.
	repair2 := reqs[2].Messages[2].Content
	for _, want := range []string{"journey buy", "check status failed: got 201, expected 200", "keep them unchanged: browse, failed-login"} {
		if !strings.Contains(repair2, want) {
			t.Errorf("repair 2 lacks %q:\n%s", want, repair2)
		}
	}
	// Nothing recorded from traffic may reach the provider unredacted.
	for i, r := range reqs {
		all := r.System
		for _, m := range r.Messages {
			all += m.Content
		}
		for _, leak := range []string{shopToken, "real.person@example.com", "555 0199"} {
			if strings.Contains(all, leak) {
				t.Errorf("request %d leaks %q", i, leak)
			}
		}
		if len(r.JSONSchema) == 0 || r.SchemaName != "scenario" {
			t.Error("requests must carry the scenario schema")
		}
	}
	// The spec digest (a document) keeps the test account for the model.
	if !strings.Contains(reqs[0].Messages[0].Content, "user1@shop.test") {
		t.Error("documents should keep test accounts")
	}

	// The proposal is valid YAML with the target filled in, and a diff.
	if !strings.Contains(res.YAML, "baseURL: "+srv.URL) || !strings.Contains(res.YAML, "# Generated by stampede generate with fake/fake-model") {
		t.Errorf("yaml:\n%s", res.YAML)
	}
	if _, err := scenario.Parse([]byte(res.YAML)); err != nil {
		t.Errorf("proposal does not parse: %v\n%s", err, res.YAML)
	}
	if !strings.Contains(res.Diff, "-metadata: {name: old}") || !strings.Contains(res.Diff, "+  name: shop-mix") {
		t.Errorf("diff:\n%s", res.Diff)
	}
	for _, s := range []string{StageUnderstand, StageDraft, StageStaticCheck, StageDryRun, StageRepair, StageDone} {
		if !strings.Contains(strings.Join(stages, ","), s) {
			t.Errorf("stage %s not reported: %v", s, stages)
		}
	}
}

func TestPipelineFlagsJourneysThatNeverPass(t *testing.T) {
	srv := shopServer(t)
	fake := &provider.Fake{Replies: []any{toJSON(t, draft2), toJSON(t, draft2)}}
	res, err := Generate(context.Background(), Inputs{OpenAPI: []byte(shopSpec)}, Options{
		Provider: fake, Target: srv.URL, DryRun: true, MaxRepairs: 1, OmitBaseURL: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Validated() || res.Fatal() {
		t.Fatalf("expected a usable but unvalidated proposal: %+v", res.Problems)
	}
	if f := res.Flagged(); len(f) != 1 || f[0] != "buy" {
		t.Fatalf("flagged: %v", f)
	}
	if !strings.Contains(res.YAML, "# FLAGGED FOR REVIEW") || !strings.Contains(res.YAML, "after 1 repair round:") {
		t.Errorf("yaml should mark the flagged journey:\n%s", res.YAML)
	}
	if strings.Contains(res.YAML, "baseURL") {
		t.Errorf("server proposals leave the base URL to the target:\n%s", res.YAML)
	}
	if len(fake.Requests()) != 2 {
		t.Errorf("calls = %d", len(fake.Requests()))
	}
}

func TestPipelineBlocksThirdPartyHosts(t *testing.T) {
	bad := strings.Replace(draft3, "post: /api/checkout", "post: https://api.stripe.com/v1/charges", 1)
	fake := &provider.Fake{Replies: []any{toJSON(t, bad)}}
	res, err := Generate(context.Background(), Inputs{Description: "shop"}, Options{Provider: fake, Target: "http://127.0.0.1:1", MaxRepairs: -1})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Fatal() {
		t.Fatal("a scenario calling Stripe must be unusable")
	}
	found := false
	for _, p := range res.Problems {
		if p.Fatal && strings.Contains(p.Message, "payment provider") && p.Journey == "buy" {
			found = true
		}
	}
	if !found {
		t.Errorf("problems: %+v", res.Problems)
	}
	if _, err := Generate(context.Background(), Inputs{Description: "x"}, Options{Provider: fake, Target: "https://api.stripe.com"}); err == nil {
		t.Error("a payment provider as the target must be refused")
	}
}

func TestPipelineWithoutDryRunAndBudget(t *testing.T) {
	fake := &provider.Fake{Replies: []any{"not json at all", toJSON(t, draft3)}, PerCall: provider.Usage{InputTokens: 50, OutputTokens: 50}}
	res, err := Generate(context.Background(), Inputs{Description: "a shop"}, Options{Provider: fake, TokenBudget: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Validated() || res.Rounds != 1 {
		t.Fatalf("problems=%v rounds=%d", res.Problems, res.Rounds)
	}
	if !strings.Contains(fake.Requests()[1].Messages[2].Content, "not a valid JSON object") {
		t.Error("the JSON problem should be sent back")
	}
	for _, j := range res.Journeys {
		if j.Status != JourneyNotRun {
			t.Errorf("%s: %s", j.Name, j.Status)
		}
	}
	if !strings.Contains(res.YAML, "baseURL: ${env.TARGET_URL}") || !strings.Contains(res.YAML, "Not dry-run") {
		t.Errorf("yaml:\n%s", res.YAML)
	}

	// A budget smaller than one call stops after the first draft.
	fake2 := &provider.Fake{Replies: []any{toJSON(t, draft1), toJSON(t, draft3)}, PerCall: provider.Usage{InputTokens: 600, OutputTokens: 600}}
	res, err = Generate(context.Background(), Inputs{Description: "a shop"}, Options{Provider: fake2, TokenBudget: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake2.Requests()) != 1 || !strings.Contains(res.Problems[len(res.Problems)-1].Message, "budget") {
		t.Errorf("calls=%d problems=%v", len(fake2.Requests()), res.Problems)
	}
}
