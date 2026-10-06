package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// gqlServer is a small GraphQL endpoint with automatic persisted queries.
type gqlServer struct {
	mu       sync.Mutex
	known    map[string]string // hash -> query
	posts    atomic.Int64
	misses   atomic.Int64
	noTraces atomic.Int64
	echoed   sync.Map
}

func (g *gqlServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/echo/") {
		g.echoed.Store(strings.TrimPrefix(r.URL.Path, "/echo/"), true)
		return
	}
	g.posts.Add(1)
	if r.Header.Get("traceparent") == "" {
		g.noTraces.Add(1)
	}
	var req struct {
		Query      string         `json:"query"`
		Variables  map[string]any `json:"variables"`
		Extensions struct {
			PersistedQuery *struct {
				Hash string `json:"sha256Hash"`
			} `json:"persistedQuery"`
		} `json:"extensions"`
	}
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if pq := req.Extensions.PersistedQuery; pq != nil {
		g.mu.Lock()
		if req.Query == "" {
			req.Query = g.known[pq.Hash]
		} else {
			sum := sha256.Sum256([]byte(req.Query))
			if hex.EncodeToString(sum[:]) != pq.Hash {
				g.mu.Unlock()
				http.Error(w, "hash mismatch", http.StatusBadRequest)
				return
			}
			g.known[pq.Hash] = req.Query
		}
		g.mu.Unlock()
		if req.Query == "" {
			g.misses.Add(1)
			fmt.Fprint(w, `{"errors":[{"message":"PersistedQueryNotFound","extensions":{"code":"PERSISTED_QUERY_NOT_FOUND"}}]}`)
			return
		}
	}
	switch {
	case strings.Contains(req.Query, "broken"):
		fmt.Fprint(w, `{"data":null,"errors":[{"message":"resolver failed"}]}`)
	case strings.Contains(req.Query, "product"):
		fmt.Fprintf(w, `{"data":{"product":{"id":%q,"name":"shoe-%v"}}}`, req.Variables["id"], req.Variables["id"])
	default:
		fmt.Fprint(w, `{"data":{}}`)
	}
}

func TestGraphQL(t *testing.T) {
	const product = `query Product($id: ID!) { product(id: $id) { id name } }`
	tests := []struct {
		name       string
		steps      string
		iterations int
		mut        func(*Options)
		wantFailed uint64
		wantErr    string
		wantPosts  int64
		wantMisses int64
		wantEcho   string
	}{
		{
			name: "query with variables feeds a later step",
			steps: fmt.Sprintf(`
      - graphql: /graphql
        query: %q
        variables: { id: "p${iter}" }
        operationName: Product
        check: { json: { "$.data.product.id": exists } }
        extract: { productName: "$.data.product.name" }
      - get: /echo/${productName}`, product),
			iterations: 2, wantPosts: 2, wantEcho: "shoe-p1",
		},
		{
			name:       "errors array fails the step",
			steps:      `[{graphql: /graphql, query: "{ broken }"}]`,
			iterations: 3, wantFailed: 3, wantErr: "graphql error", wantPosts: 3,
		},
		{
			name:       "allowErrors accepts errors",
			steps:      `[{graphql: /graphql, query: "{ broken }", check: {allowErrors: true, json: {"$.errors[0].message": resolver failed}}}]`,
			iterations: 2, wantPosts: 2,
		},
		{
			name: "persisted query misses once then hits",
			steps: fmt.Sprintf(`
      - graphql: /graphql
        query: %q
        variables: { id: "x" }
        persisted: true
        check: { json: { "$.data.product.name": shoe-x } }`, product),
			// One VU: the first iteration misses and resends, the rest hit.
			iterations: 3, wantPosts: 4, wantMisses: 1,
		},
		{
			name:       "unknown persisted hash without a query fails",
			steps:      fmt.Sprintf(`[{graphql: /graphql, persisted: {sha256: %q}}]`, strings.Repeat("ab", 32)),
			iterations: 1, wantFailed: 1, wantErr: "graphql persisted query not found", wantPosts: 1, wantMisses: 1,
		},
		{
			name:       "safety policy applies",
			steps:      `[{graphql: /graphql, query: "{ x }"}]`,
			iterations: 2, wantFailed: 2, wantErr: "blocked by safety",
			mut: func(o *Options) { o.AllowHost = func(*url.URL) bool { return false } },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := &gqlServer{known: map[string]string{}}
			srv := httptest.NewServer(g)
			defer srv.Close()
			out := run(t, fmt.Sprintf(`
metadata: {name: gql}
target: {baseURL: %q}
journeys:
  - name: a
    steps: %s
load: {iterations: %d, vus: 1}`, srv.URL, tc.steps, tc.iterations), tc.mut)
			st := out.total.Steps[0]
			if st == nil || st.Failed != tc.wantFailed || (tc.wantErr != "" && st.Errors[tc.wantErr] != tc.wantFailed) {
				t.Fatalf("step stats %+v, want %d failures labelled %q", st, tc.wantFailed, tc.wantErr)
			}
			if g.posts.Load() != tc.wantPosts || g.misses.Load() != tc.wantMisses {
				t.Errorf("server saw %d posts and %d misses, want %d and %d", g.posts.Load(), g.misses.Load(), tc.wantPosts, tc.wantMisses)
			}
			if g.noTraces.Load() != 0 {
				t.Errorf("%d requests lacked traceparent", g.noTraces.Load())
			}
			if tc.wantEcho != "" {
				if _, ok := g.echoed.Load(tc.wantEcho); !ok {
					t.Errorf("extracted variable did not reach the next step (want /echo/%s)", tc.wantEcho)
				}
			}
		})
	}
}
