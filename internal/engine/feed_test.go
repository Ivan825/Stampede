package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Ivan825/Stampede/internal/scenario"
)

func TestGeneratedData(t *testing.T) {
	var mu sync.Mutex
	emails := map[string]bool{}
	sizes := map[int]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Email, Note string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		emails[body.Email] = true
		sizes[len(body.Note)]++
		mu.Unlock()
	}))
	defer srv.Close()
	out := run(t, fmt.Sprintf(`
metadata: {name: gen}
target: {baseURL: %q}
data:
  people: {generate: {email: email, note: "text(256KB)"}}
journeys: [{name: a, steps: [{post: /signup, json: {email: "${data.people.email}", note: "${data.people.note}"}}]}]
load: {iterations: 40, vus: 4}`, srv.URL), nil)
	if got := out.total.Totals(); got.Requests != 40 || got.Failed != 0 {
		t.Fatalf("requests %d failed %d %v", got.Requests, got.Failed, got.Errors)
	}
	if len(emails) != 40 || sizes[256<<10] != 40 {
		t.Errorf("%d distinct emails, sizes %v", len(emails), sizes)
	}
}

func TestSQLDataHostPolicy(t *testing.T) {
	src := `
metadata: {name: sqlfeed}
target: {baseURL: "http://localhost"}
data:
  users: {sql: {driver: postgres, dsn: "postgres://u:p@db.example.com:5432/app", query: "SELECT 1"}}
journeys: [{name: a, steps: [{get: "/${data.users.id}"}]}]
load: {iterations: 1}`
	s, err := scenario.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	prog, _ := scenario.Compile(s)
	plan, _ := s.Load.Plan()
	_, err = New(Options{Program: prog, Plan: plan, AllowHost: func(u *url.URL) bool { return u.Hostname() == "localhost" }})
	if err == nil || !strings.Contains(err.Error(), "db.example.com:5432 is not allowed") {
		t.Fatalf("err = %v", err)
	}
}

func TestSQLData(t *testing.T) {
	dsn := os.Getenv("STAMPEDE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("STAMPEDE_TEST_DATABASE_URL not set")
	}
	var mu sync.Mutex
	seen := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen[r.URL.Path+string(b)] = true
		mu.Unlock()
	}))
	defer srv.Close()
	out := run(t, fmt.Sprintf(`
metadata: {name: sqlfeed}
target: {baseURL: %q}
data:
  users: {sql: {driver: postgres, dsn: "${secret.DB}", query: "SELECT g AS id FROM generate_series(1, 20) g"}, mode: unique}
journeys: [{name: a, steps: [{get: "/u/${data.users.id}"}]}]
load: {vus: 2, duration: 5s}`, srv.URL), func(o *Options) { o.Secrets = map[string]string{"DB": dsn} })
	if out.total.Totals().Requests != 20 || len(seen) != 20 || !seen["/u/20"] {
		t.Fatalf("requests %d, paths %v", out.total.Totals().Requests, seen)
	}
}
