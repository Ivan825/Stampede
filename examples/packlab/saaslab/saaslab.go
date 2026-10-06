// Package saaslab is SaaSLab, a multi-tenant B2B app: sign-in, records
// that users create and edit, heavy reports, a REST API and a GraphQL API
// (with automatic persisted queries) for dashboards. It is the reference
// app for the saas pack.
//
// Its data lives in memory behind a simulated database: each store query
// costs a fixed round trip (300µs by default), so query counts show up as
// latency the way they do against a real database.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - reports: every report scans the tenant's records and all tenants
//     share two report workers, so one big tenant running reports makes
//     everyone else's wait (fix "reports" gives each tenant its own
//     worker, so a noisy tenant only queues behind itself).
//   - n1: GraphQL loads each record's owner with its own query, so a page
//     of 20 records costs 21 round trips (fix "n1" loads owners in one
//     batch).
//   - count: record lists count every matching record of the tenant on
//     each page to report a total, so lists slow down with tenant size
//     (fix "count" keeps the counts up to date).
package saaslab

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

// Password is every seeded user's password.
const Password = "saaslab-pass"

// Tenants: megacorp is the big, noisy one; the rest are small.
var Tenants = []string{"megacorp", "acme", "globex", "initech", "umbrella", "hooli", "stark", "wayne", "wonka",
	"tyrell", "cyberdyne", "soylent", "aperture", "monarch", "vandelay", "dunder", "pied-piper", "gringotts", "oscorp", "krusty"}

var statuses = []string{"open", "won", "lost"}

type user struct {
	ID     int    `json:"id"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Tenant string `json:"tenant"`
	hash   [32]byte
}

type record struct {
	ID      string
	Tenant  string
	Title   string
	Status  string
	Amount  float64
	Owner   int
	Updated time.Time
}

func (r *record) json() map[string]any {
	return map[string]any{"id": r.ID, "title": r.Title, "status": r.Status, "amount": r.Amount, "ownerId": r.Owner,
		"updated": r.Updated.UTC().Format(time.RFC3339)}
}

type tenant struct {
	name    string
	mu      sync.RWMutex
	records []*record
	byID    map[string]*record
	counts  map[string]int // by status, with the count fix
	worker  chan struct{}  // with the reports fix
}

type session struct {
	user   *user
	tenant *tenant
}

type server struct {
	cfg       labkit.Config
	latency   time.Duration
	queries   labkit.Counter
	tenants   map[string]*tenant
	users     map[string]*user
	usersByID map[int]*user
	sessions  sync.Map
	workers   chan struct{}
	scans     labkit.Counter
	reporting labkit.Gauge
	nextID    sync.Mutex
	seq       int
	apqMu     sync.Mutex
	apq       map[string]string
}

// New returns SaaSLab's handler.
func New(cfg labkit.Config) http.Handler {
	_, h := newServer(cfg)
	return h
}

func newServer(cfg labkit.Config) (*server, http.Handler) {
	s := &server{cfg: cfg, latency: 300 * time.Microsecond, tenants: map[string]*tenant{}, users: map[string]*user{},
		usersByID: map[int]*user{}, workers: make(chan struct{}, 2), apq: map[string]string{}}
	if cfg.Fast {
		s.latency = 0
	}
	s.seed()
	routes := []labkit.Route{
		{Method: "POST", Path: "/api/login", Tag: "sessions", Summary: "Sign in"},
		{Method: "GET", Path: "/api/me", Tag: "sessions", Summary: "The signed-in user and tenant"},
		{Method: "GET", Path: "/api/records", Tag: "records", Summary: "List records (page, limit, status)"},
		{Method: "POST", Path: "/api/records", Tag: "records", Summary: "Create a record"},
		{Method: "GET", Path: "/api/records/{id}", Tag: "records", Summary: "Get a record"},
		{Method: "PATCH", Path: "/api/records/{id}", Tag: "records", Summary: "Edit a record"},
		{Method: "GET", Path: "/api/reports/{name}", Tag: "reports", Summary: "Run a report: pipeline, revenue-by-month, activity"},
		{Method: "GET", Path: "/api/tenants/current", Tag: "tenants", Summary: "The current tenant"},
		{Method: "POST", Path: "/graphql", Tag: "graphql", Summary: "GraphQL API for dashboards"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "SaaSLab", "links": map[string]string{"openapi": "/openapi.json", "graphql": "/graphql"}})
	})
	mux.Handle("GET /openapi.json", labkit.OpenAPI("SaaSLab", "Multi-tenant B2B app with REST and GraphQL APIs and planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /api/login", s.login)
	mux.Handle("GET /api/me", s.authed(s.me))
	mux.Handle("GET /api/tenants/current", s.authed(s.currentTenant))
	mux.Handle("GET /api/records", s.authed(s.listRecords))
	mux.Handle("POST /api/records", s.authed(s.createRecord))
	mux.Handle("GET /api/records/{id}", s.authed(s.getRecord))
	mux.Handle("PATCH /api/records/{id}", s.authed(s.editRecord))
	mux.Handle("GET /api/reports/{name}", s.authed(s.report))
	mux.Handle("POST /graphql", s.authed(s.graphql))
	return s, mux
}

// seed creates 20 tenants with 25 users each; megacorp has 100,000
// records and the others 1,000.
func (s *server) seed() {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	uid := 0
	for _, name := range Tenants {
		t := &tenant{name: name, byID: map[string]*record{}, counts: map[string]int{}, worker: make(chan struct{}, 1)}
		s.tenants[name] = t
		var owners []int
		for i := 1; i <= 25; i++ {
			uid++
			email := fmt.Sprintf("user%02d@%s.test", i, name)
			u := &user{ID: uid, Email: email, Name: fmt.Sprintf("User %d of %s", i, name), Tenant: name, hash: sha256.Sum256([]byte(name + Password))}
			s.users[email] = u
			s.usersByID[uid] = u
			owners = append(owners, uid)
		}
		n := 1000
		if name == "megacorp" {
			n = 100000
		}
		t.records = make([]*record, 0, n)
		for i := 1; i <= n; i++ {
			r := &record{ID: fmt.Sprintf("rec_%s_%d", name, i), Tenant: name, Title: fmt.Sprintf("Deal %d", i),
				Status: statuses[i%3], Amount: float64(i%97) * 125, Owner: owners[i%len(owners)], Updated: base.Add(time.Duration(i%300) * 24 * time.Hour)}
			t.records = append(t.records, r)
			t.byID[r.ID] = r
			t.counts[r.Status]++
		}
	}
}

// query simulates one database round trip.
func (s *server) query() {
	s.queries.Add(1)
	if s.latency > 0 {
		time.Sleep(s.latency)
	}
}

type handler func(w http.ResponseWriter, r *http.Request, ss *session)

func (s *server) authed(h handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := s.sessions.Load(labkit.Bearer(r))
		if !ok {
			labkit.Error(w, 401, "unauthorized", "sign in and send Authorization: Bearer <token>")
			return
		}
		s.query() // session lookup
		h(w, r, v.(*session))
	})
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password string }
	if !labkit.Decode(w, r, &in) {
		return
	}
	s.query()
	u := s.users[strings.ToLower(in.Email)]
	ok := false
	if u != nil {
		h := sha256.Sum256([]byte(u.Tenant + in.Password))
		ok = subtle.ConstantTimeCompare(u.hash[:], h[:]) == 1
	}
	if !ok {
		labkit.Error(w, 401, "invalid_credentials", "wrong email or password")
		return
	}
	tok := labkit.Token("saas_")
	s.sessions.Store(tok, &session{user: u, tenant: s.tenants[u.Tenant]})
	labkit.JSON(w, 200, map[string]any{"token": tok, "user": u, "tenant": u.Tenant})
}

func (s *server) me(w http.ResponseWriter, _ *http.Request, ss *session) {
	labkit.JSON(w, 200, map[string]any{"user": ss.user, "tenant": ss.tenant.name})
}

func (s *server) currentTenant(w http.ResponseWriter, _ *http.Request, ss *session) {
	t := ss.tenant
	t.mu.RLock()
	n := len(t.records)
	t.mu.RUnlock()
	labkit.JSON(w, 200, map[string]any{"name": t.name, "records": n, "users": 25})
}

// count returns how many of the tenant's records have a status (all when
// empty); the caller holds t.mu.
func (s *server) count(t *tenant, status string) int {
	s.query()
	if s.cfg.Fixes.On("count") {
		if status == "" {
			return len(t.records)
		}
		return t.counts[status]
	}
	// Bottleneck: COUNT(*) over the tenant's whole table.
	n := 0
	for _, r := range t.records {
		if status == "" || r.Status == status {
			n++
		}
	}
	s.scans.Add(len(t.records))
	return n
}

func (s *server) page(t *tenant, status string, offset, limit int) []*record {
	s.query()
	var out []*record
	seen := 0
	for i := len(t.records) - 1; i >= 0 && len(out) < limit; i-- { // newest first
		r := t.records[i]
		if status != "" && r.Status != status {
			continue
		}
		if seen >= offset {
			out = append(out, r)
		}
		seen++
	}
	return out
}

func (s *server) listRecords(w http.ResponseWriter, r *http.Request, ss *session) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	pg, _ := strconv.Atoi(q.Get("page"))
	if pg < 1 {
		pg = 1
	}
	status := q.Get("status")
	t := ss.tenant
	t.mu.RLock()
	recs := s.page(t, status, (pg-1)*limit, limit)
	total := s.count(t, status)
	out := make([]map[string]any, len(recs))
	for i, rec := range recs {
		out[i] = rec.json()
	}
	t.mu.RUnlock()
	labkit.JSON(w, 200, map[string]any{"records": out, "page": pg, "total": total})
}

func (s *server) find(ss *session, id string) *record {
	s.query()
	t := ss.tenant
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.byID[id] // other tenants' records are invisible
}

func (s *server) getRecord(w http.ResponseWriter, r *http.Request, ss *session) {
	rec := s.find(ss, r.PathValue("id"))
	if rec == nil {
		labkit.Error(w, 404, "not_found", "no such record")
		return
	}
	ss.tenant.mu.RLock()
	out := rec.json()
	ss.tenant.mu.RUnlock()
	labkit.JSON(w, 200, out)
}

type recordInput struct {
	Title  *string  `json:"title"`
	Status *string  `json:"status"`
	Amount *float64 `json:"amount"`
}

func validStatus(st string) bool {
	for _, x := range statuses {
		if x == st {
			return true
		}
	}
	return false
}

// create adds a record for the session's user.
func (s *server) create(ss *session, in recordInput) (*record, string) {
	if in.Title == nil || strings.TrimSpace(*in.Title) == "" {
		return nil, "title is required"
	}
	st := "open"
	if in.Status != nil {
		st = *in.Status
	}
	if !validStatus(st) {
		return nil, "status must be open, won or lost"
	}
	amount := 0.0
	if in.Amount != nil {
		amount = *in.Amount
	}
	s.nextID.Lock()
	s.seq++
	n := s.seq
	s.nextID.Unlock()
	t := ss.tenant
	rec := &record{ID: fmt.Sprintf("rec_%s_new%d", t.name, n), Tenant: t.name, Title: *in.Title, Status: st, Amount: amount, Owner: ss.user.ID, Updated: time.Now()}
	s.query()
	t.mu.Lock()
	t.records = append(t.records, rec)
	t.byID[rec.ID] = rec
	t.counts[st]++
	t.mu.Unlock()
	return rec, ""
}

// edit changes a record; it returns nil when the record is not found.
func (s *server) edit(ss *session, id string, in recordInput) (*record, string) {
	rec := s.find(ss, id)
	if rec == nil {
		return nil, ""
	}
	if in.Status != nil && !validStatus(*in.Status) {
		return nil, "status must be open, won or lost"
	}
	s.query()
	t := ss.tenant
	t.mu.Lock()
	defer t.mu.Unlock()
	if in.Title != nil {
		rec.Title = *in.Title
	}
	if in.Status != nil {
		t.counts[rec.Status]--
		rec.Status = *in.Status
		t.counts[rec.Status]++
	}
	if in.Amount != nil {
		rec.Amount = *in.Amount
	}
	rec.Updated = time.Now()
	return rec, ""
}

func (s *server) createRecord(w http.ResponseWriter, r *http.Request, ss *session) {
	var in recordInput
	if !labkit.Decode(w, r, &in) {
		return
	}
	rec, problem := s.create(ss, in)
	if rec == nil {
		labkit.Error(w, 422, "invalid_record", problem)
		return
	}
	ss.tenant.mu.RLock()
	out := rec.json()
	ss.tenant.mu.RUnlock()
	labkit.JSON(w, 201, out)
}

func (s *server) editRecord(w http.ResponseWriter, r *http.Request, ss *session) {
	var in recordInput
	if !labkit.Decode(w, r, &in) {
		return
	}
	rec, problem := s.edit(ss, r.PathValue("id"), in)
	switch {
	case problem != "":
		labkit.Error(w, 422, "invalid_record", problem)
	case rec == nil:
		labkit.Error(w, 404, "not_found", "no such record")
	default:
		ss.tenant.mu.RLock()
		out := rec.json()
		ss.tenant.mu.RUnlock()
		labkit.JSON(w, 200, out)
	}
}

// runReport computes a report over the tenant's records.
func (s *server) runReport(ss *session, name string) (map[string]any, bool) {
	if name != "pipeline" && name != "revenue-by-month" && name != "activity" {
		return nil, false
	}
	slot := s.workers // bottleneck: two workers shared by every tenant
	if s.cfg.Fixes.On("reports") {
		slot = ss.tenant.worker
	}
	slot <- struct{}{}
	defer func() { <-slot }()
	defer s.reporting.Enter()()
	s.query()
	t := ss.tenant
	t.mu.RLock()
	defer t.mu.RUnlock()
	s.scans.Add(len(t.records))
	rows := map[string]map[string]float64{}
	add := func(key, field string, v float64) {
		if rows[key] == nil {
			rows[key] = map[string]float64{}
		}
		rows[key][field] += v
	}
	for _, r := range t.records {
		switch name {
		case "pipeline":
			add(r.Status, "count", 1)
			add(r.Status, "amount", r.Amount)
		case "revenue-by-month":
			if r.Status == "won" {
				add(r.Updated.Format("2006-01"), "revenue", r.Amount)
			}
		case "activity":
			add(strconv.Itoa(r.Owner), "records", 1)
		}
	}
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		row := map[string]any{"key": k}
		for f, v := range rows[k] {
			row[f] = v
		}
		out = append(out, row)
	}
	return map[string]any{"report": name, "tenant": t.name, "records": len(t.records), "rows": out}, true
}

func (s *server) report(w http.ResponseWriter, r *http.Request, ss *session) {
	out, ok := s.runReport(ss, r.PathValue("name"))
	if !ok {
		labkit.Error(w, 404, "not_found", "reports: pipeline, revenue-by-month, activity")
		return
	}
	labkit.JSON(w, 200, out)
}
