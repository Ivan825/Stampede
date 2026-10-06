package saaslab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

func start(t *testing.T, fixes string, latency time.Duration) (*server, string) {
	s, h := newServer(labkit.Config{Fast: true, Fixes: labkit.ParseFixes(fixes)})
	s.latency = latency
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return s, srv.URL
}

func call(t *testing.T, method, u, token, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, u, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Error(err)
		return 0, nil
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func login(t *testing.T, base, email string) string {
	t.Helper()
	code, m := call(t, "POST", base+"/api/login", "", `{"email":"`+email+`","password":"`+Password+`"}`)
	if code != 200 {
		t.Fatalf("login %s: %d %v", email, code, m)
	}
	return m["token"].(string)
}

func gql(t *testing.T, base, token, body string) map[string]any {
	t.Helper()
	code, m := call(t, "POST", base+"/graphql", token, body)
	if code != 200 {
		t.Fatalf("graphql: %d %v", code, m)
	}
	return m
}

func TestGraphQL(t *testing.T) {
	_, base := start(t, "", 0)
	tok := login(t, base, "user01@acme.test")
	m := gql(t, base, tok, `{"query":"mutation { createRecord(title: \"x\", amount: 5) { id status } }"}`)
	rec := m["data"].(map[string]any)["createRecord"].(map[string]any)
	if rec["status"] != "open" {
		t.Fatalf("create: %v", m)
	}
	m = gql(t, base, tok, `{"query":"mutation W($id: ID!) { updateRecord(id: $id, status: \"won\") { s: status } }","variables":{"id":"`+rec["id"].(string)+`"}}`)
	if m["data"].(map[string]any)["updateRecord"].(map[string]any)["s"] != "won" {
		t.Fatalf("update with alias: %v", m)
	}
	// Another tenant's record is invisible.
	m = gql(t, base, tok, `{"query":"{ record(id: \"rec_megacorp_1\") { id } }"}`)
	if m["data"].(map[string]any)["record"] != nil {
		t.Fatalf("cross-tenant read: %v", m)
	}
	if m = gql(t, base, tok, `{"query":"{ nope }"}`); m["errors"] == nil {
		t.Fatalf("unknown field: %v", m)
	}
}

func TestPersistedQueries(t *testing.T) {
	_, base := start(t, "", 0)
	tok := login(t, base, "user01@acme.test")
	q := "{ dashboard { tenant } }"
	sum := sha256.Sum256([]byte(q))
	hash := hex.EncodeToString(sum[:])
	ext := `"extensions":{"persistedQuery":{"version":1,"sha256Hash":"` + hash + `"}}`
	m := gql(t, base, tok, `{`+ext+`}`)
	if errs, _ := m["errors"].([]any); len(errs) == 0 || errs[0].(map[string]any)["message"] != "PersistedQueryNotFound" {
		t.Fatalf("first hash-only request: %v", m)
	}
	gql(t, base, tok, `{"query":"`+q+`",`+ext+`}`)
	m = gql(t, base, tok, `{`+ext+`}`)
	if m["data"].(map[string]any)["dashboard"].(map[string]any)["tenant"] != "acme" {
		t.Fatalf("hash-only after registering: %v", m)
	}
}

func TestN1Bottleneck(t *testing.T) {
	for _, tc := range []struct {
		fixes string
		want  int64
	}{{"", 22}, {"n1", 3}} {
		s, base := start(t, tc.fixes, 0)
		tok := login(t, base, "user01@acme.test")
		before := s.queries.Load()
		gql(t, base, tok, `{"query":"{ records(first: 20) { id owner { name } } }"}`)
		// session + page + one per owner (or one batch)
		if got := s.queries.Load() - before; got != tc.want {
			t.Errorf("fixes %q: %d queries for a page of 20, want %d", tc.fixes, got, tc.want)
		}
	}
}

func TestCountBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "count"} {
		s, base := start(t, fixes, 0)
		tok := login(t, base, "user01@megacorp.test")
		before := s.scans.Load()
		if code, m := call(t, "GET", base+"/api/records?limit=5", tok, ""); code != 200 || m["total"].(float64) != 100000 {
			t.Fatalf("list: %d %v", code, m["total"])
		}
		got := s.scans.Load() - before
		if fixes == "" && got != 100000 {
			t.Errorf("without the fix a page counts all 100,000 records: %d", got)
		}
		if fixes != "" && got != 0 {
			t.Errorf("with the fix nothing is counted: %d", got)
		}
	}
}

func TestReportsBottleneck(t *testing.T) {
	for _, tc := range []struct {
		fixes  string
		atMost int64
	}{{"", 2}, {"reports", 20}} {
		s, base := start(t, tc.fixes, 20*time.Millisecond)
		var wg sync.WaitGroup
		for _, ten := range Tenants[:8] {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tok := login(t, base, "user01@"+ten+".test")
				call(t, "GET", base+"/api/reports/pipeline", tok, "")
			}()
		}
		wg.Wait()
		p := s.reporting.Peak()
		if p > tc.atMost || (tc.fixes != "" && p <= 2) {
			t.Errorf("fixes %q: %d reports ran at once", tc.fixes, p)
		}
	}
}
