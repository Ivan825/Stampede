// Package mobilelab is MobileLab, the backend of a mobile to-do and
// habits app, shaped like most mobile backends: remote config and feature
// flags fetched at launch, a minimum app version, device registration for
// push, delta sync of the user's items with offline changes, batched
// analytics events, and a GraphQL endpoint with persisted queries for the
// home screen. It is the reference app for the mobile-backends pack.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - config: every remote-config request parses the whole config document
//     (300 flags with their targeting rules, about 700 KB) again and
//     evaluates it, and the answer cannot be cached, so
//     a push that makes everyone open the app at once melts the config
//     endpoint (fix "config" parses once, caches the evaluated config per
//     platform, version and rollout bucket, and answers If-None-Match
//     with 304).
//   - sync: delta sync ignores the client's sync token and sends every
//     item the user has (up to 1,000) on every launch (fix "sync" sends
//     only what changed since the token).
//   - events: each analytics event is written on its own (200µs) under one
//     global lock, so devices flushing big batches queue behind each other
//     (fix "events" writes each batch at once, outside the lock).
package mobilelab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

const (
	// Users is how many accounts MobileLab has (u00001 ... u20000).
	Users = 20000
	// Password is every account's password.
	Password = "mobilelab-pass"
	// MinVersion is the oldest app version the API still serves.
	MinVersion = "3.0.0"
	// LatestVersion is the newest app version.
	LatestVersion = "4.2.0"

	flags = 300
)

type item struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Done    bool   `json:"done"`
	Deleted bool   `json:"deleted,omitempty"`
	Version int64  `json:"version"`
}

type account struct {
	id      string
	name    string
	mu      sync.Mutex
	items   []*item // loaded on first use
	byID    map[string]*item
	streak  int
	devices map[string]string // device id to push token
}

// flagRule is one feature flag in the config document.
type flagRule struct {
	Name       string      `json:"name"`
	Platforms  []string    `json:"platforms"`
	MinVersion string      `json:"minVersion"`
	Rollout    int         `json:"rollout"` // percent of users
	Value      any         `json:"value"`
	Targeting  []targeting `json:"targeting"`
}

// targeting is a rule for segments of users. MobileLab carries them, as
// real config documents do, but every user here is in the default
// segment, so they never change an answer.
type targeting struct {
	Countries []string `json:"countries"`
	Segments  []string `json:"segments"`
	Value     any      `json:"value"`
}

type configDoc struct {
	RefreshSeconds int        `json:"refreshSeconds"`
	Flags          []flagRule `json:"flags"`
}

type cachedConfig struct {
	body []byte
	etag string
}

type server struct {
	cfg       labkit.Config
	itemScale int
	write     time.Duration

	doc    []byte // the config document as stored
	parsed *configDoc
	pOnce  sync.Once
	ccMu   sync.Mutex
	cc     map[string]cachedConfig

	amu      sync.Mutex
	accounts map[string]*account
	version  atomic.Int64

	smu      sync.RWMutex
	sessions map[string]*account

	emu    sync.Mutex // the analytics store's one lock (events bottleneck)
	events int64

	apqMu sync.Mutex
	apq   map[string]string

	parses     labkit.Counter // config documents parsed
	serialized labkit.Counter // items sent by sync
	writes     labkit.Counter // analytics writes
}

// New returns MobileLab's handler.
func New(cfg labkit.Config) http.Handler {
	_, h := newServer(cfg)
	return h
}

var countries = strings.Fields("IN US GB DE FR BR JP ID MX NG PK BD RU ES IT CA AU KR TR NL SE PL AR ZA EG VN PH TH MY SA")

func newServer(cfg labkit.Config) (*server, http.Handler) {
	s := &server{cfg: cfg, itemScale: 1, write: 200 * time.Microsecond, cc: map[string]cachedConfig{},
		accounts: map[string]*account{}, sessions: map[string]*account{}, apq: map[string]string{}}
	if cfg.Fast {
		s.itemScale, s.write = 10, 2*time.Microsecond
	}
	s.version.Store(1000)
	rules := 8 // targeting rules per flag
	if cfg.Fast {
		rules = 1
	}
	doc := configDoc{RefreshSeconds: 300}
	for i := range flags {
		f := flagRule{Name: fmt.Sprintf("feature_%03d", i), Rollout: (i * 37) % 101, MinVersion: []string{"3.0.0", "3.5.0", "4.0.0", "4.2.0"}[i%4],
			Platforms: [][]string{{"ios", "android"}, {"ios"}, {"android"}}[i%3], Value: i%5 != 0}
		if i%7 == 0 {
			f.Value = map[string]any{"variant": []string{"control", "blue", "green"}[i%3], "limit": 10 + i}
		}
		for k := range rules {
			t := targeting{Value: k%2 == 0}
			for c := range 20 {
				t.Countries = append(t.Countries, countries[(i+k+c)%len(countries)])
			}
			for g := range 10 {
				t.Segments = append(t.Segments, fmt.Sprintf("segment-%04d", (i*31+k*7+g)%5000))
			}
			f.Targeting = append(f.Targeting, t)
		}
		doc.Flags = append(doc.Flags, f)
	}
	s.doc, _ = json.Marshal(doc)
	routes := []labkit.Route{
		{Method: "GET", Path: "/api/v1/config", Tag: "remote-config", Summary: "Remote config and feature flags for this platform and app version"},
		{Method: "POST", Path: "/api/v1/auth/login", Tag: "auth", Summary: "Sign in; returns access and refresh tokens"},
		{Method: "POST", Path: "/api/v1/auth/refresh", Tag: "auth", Summary: "A new access token from a refresh token"},
		{Method: "GET", Path: "/api/v1/me", Tag: "profile", Summary: "The signed-in user"},
		{Method: "POST", Path: "/api/v1/devices", Tag: "devices", Summary: "Register a device for push notifications"},
		{Method: "POST", Path: "/api/v1/sync", Tag: "sync", Summary: "Delta sync: send offline changes, get changes since the sync token"},
		{Method: "POST", Path: "/api/v1/events", Tag: "analytics", Summary: "A batch of analytics events"},
		{Method: "POST", Path: "/api/v1/graphql", Tag: "graphql", Summary: "GraphQL (home screen, items); automatic persisted queries"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "MobileLab", "links": map[string]string{"openapi": "/openapi.json"}})
	})
	mux.Handle("GET /openapi.json", labkit.OpenAPI("MobileLab", "A mobile app's backend: remote config, delta sync, analytics batches and GraphQL, with planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/v1/config", s.config)
	mux.HandleFunc("POST /api/v1/auth/login", s.gate(s.login))
	mux.HandleFunc("POST /api/v1/auth/refresh", s.gate(s.refresh))
	mux.HandleFunc("GET /api/v1/me", s.gate(s.auth(s.me)))
	mux.HandleFunc("POST /api/v1/devices", s.gate(s.auth(s.device)))
	mux.HandleFunc("POST /api/v1/sync", s.gate(s.auth(s.sync)))
	mux.HandleFunc("POST /api/v1/events", s.gate(s.auth(s.ingest)))
	mux.HandleFunc("POST /api/v1/graphql", s.gate(s.auth(s.graphql)))
	return s, mux
}

// --- versions and sessions ------------------------------------------------

// older reports whether version a is below b (major.minor.patch).
func older(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range 3 {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// gate refuses app versions the API no longer serves.
func (s *server) gate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := r.Header.Get("X-App-Version")
		if v == "" || older(v, MinVersion) {
			labkit.JSON(w, http.StatusUpgradeRequired, map[string]any{"error": map[string]string{"code": "upgrade_required",
				"message": "this version of the app is no longer supported; update to continue"}, "minVersion": MinVersion, "latestVersion": LatestVersion})
			return
		}
		next(w, r)
	}
}

func (s *server) account(id string) *account {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "u"))
	if !strings.HasPrefix(id, "u") || err != nil || n < 1 || n > Users || len(id) != 6 {
		return nil
	}
	s.amu.Lock()
	defer s.amu.Unlock()
	a := s.accounts[id]
	if a == nil {
		a = &account{id: id, name: "User " + strconv.Itoa(n), streak: n % 40, devices: map[string]string{}}
		s.accounts[id] = a
	}
	return a
}

// load fills an account's items on first use: 20 to 1,000 of them (a
// tenth in fast mode). The caller holds a.mu.
func (s *server) load(a *account) {
	if a.items != nil {
		return
	}
	n, _ := strconv.Atoi(a.id[1:])
	count := (20 + (n*7919)%981) / s.itemScale
	a.byID = map[string]*item{}
	words := []string{"Drink water", "Read 20 pages", "Walk 8,000 steps", "Stretch", "Call mum", "Plan tomorrow", "Practise guitar", "Meditate"}
	for i := range max(2, count) {
		it := &item{ID: fmt.Sprintf("it_%s_%d", a.id, i+1), Title: words[i%len(words)], Done: i%3 == 0, Version: int64(1 + i%900)}
		a.items = append(a.items, it)
		a.byID[it.ID] = it
	}
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UserID   string `json:"userId"`
		Password string `json:"password"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	a := s.account(in.UserID)
	if a == nil || in.Password != Password {
		labkit.Error(w, 401, "invalid_credentials", "unknown user or wrong password")
		return
	}
	s.issue(w, a)
}

func (s *server) issue(w http.ResponseWriter, a *account) {
	access, refresh := labkit.Token("at_"), labkit.Token("rt_")
	s.smu.Lock()
	s.sessions[access] = a
	s.sessions[refresh] = a
	s.smu.Unlock()
	labkit.JSON(w, 200, map[string]any{"accessToken": access, "refreshToken": refresh, "expiresIn": 3600, "userId": a.id})
}

func (s *server) refresh(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RefreshToken string `json:"refreshToken"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	s.smu.RLock()
	a := s.sessions[in.RefreshToken]
	s.smu.RUnlock()
	if a == nil || !strings.HasPrefix(in.RefreshToken, "rt_") {
		labkit.Error(w, 401, "invalid_grant", "unknown refresh token")
		return
	}
	s.issue(w, a)
}

func (s *server) auth(next func(http.ResponseWriter, *http.Request, *account)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := labkit.Bearer(r)
		s.smu.RLock()
		a := s.sessions[tok]
		s.smu.RUnlock()
		if a == nil || !strings.HasPrefix(tok, "at_") {
			labkit.Error(w, 401, "unauthorized", "send Authorization: Bearer <access token>")
			return
		}
		next(w, r, a)
	}
}

func (s *server) me(w http.ResponseWriter, _ *http.Request, a *account) {
	a.mu.Lock()
	s.load(a)
	out := map[string]any{"id": a.id, "name": a.name, "streak": a.streak, "items": len(a.items), "devices": len(a.devices)}
	a.mu.Unlock()
	labkit.JSON(w, 200, out)
}

func (s *server) device(w http.ResponseWriter, r *http.Request, a *account) {
	var in struct {
		DeviceID  string `json:"deviceId"`
		Platform  string `json:"platform"`
		PushToken string `json:"pushToken"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	if in.DeviceID == "" || in.PushToken == "" || (in.Platform != "ios" && in.Platform != "android") {
		labkit.Error(w, 422, "bad_device", "give deviceId, platform (ios or android) and pushToken")
		return
	}
	a.mu.Lock()
	_, known := a.devices[in.DeviceID]
	a.devices[in.DeviceID] = in.PushToken
	a.mu.Unlock()
	status := 201
	if known {
		status = 200
	}
	labkit.JSON(w, status, map[string]any{"deviceId": in.DeviceID, "registered": true})
}

// --- remote config --------------------------------------------------------

func bucket(id string) int {
	h := fnv.New32a()
	h.Write([]byte(id))
	return int(h.Sum32() % 100)
}

func (s *server) evaluate(doc *configDoc, platform, version string, b int) []byte {
	out := map[string]any{}
	for _, f := range doc.Flags {
		on := b < f.Rollout && !older(version, f.MinVersion)
		ok := false
		for _, p := range f.Platforms {
			ok = ok || p == platform
		}
		if on && ok {
			out[f.Name] = f.Value
		} else {
			out[f.Name] = false
		}
	}
	body, _ := json.Marshal(map[string]any{"minVersion": MinVersion, "latestVersion": LatestVersion, "refreshSeconds": doc.RefreshSeconds,
		"upgradeAvailable": older(version, LatestVersion), "features": out})
	return body
}

func (s *server) config(w http.ResponseWriter, r *http.Request) {
	platform := r.Header.Get("X-Platform")
	version := r.Header.Get("X-App-Version")
	if (platform != "ios" && platform != "android") || version == "" {
		labkit.Error(w, 400, "bad_client", "send X-Platform (ios or android) and X-App-Version")
		return
	}
	who := r.Header.Get("X-Device-Id")
	if who == "" {
		who = "anonymous"
	}
	b := bucket(who)
	if !s.cfg.Fixes.On("config") {
		// Bottleneck: parse the whole document and evaluate it, for every
		// launch, and forbid caching.
		var doc configDoc
		_ = json.Unmarshal(s.doc, &doc)
		s.parses.Add(1)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.Write(s.evaluate(&doc, platform, version, b))
		return
	}
	s.pOnce.Do(func() {
		var doc configDoc
		_ = json.Unmarshal(s.doc, &doc)
		s.parses.Add(1)
		s.parsed = &doc
	})
	key := fmt.Sprintf("%s/%s/%d", platform, version, b)
	s.ccMu.Lock()
	c, ok := s.cc[key]
	s.ccMu.Unlock()
	if !ok {
		body := s.evaluate(s.parsed, platform, version, b)
		sum := sha256.Sum256(body)
		c = cachedConfig{body: body, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
		s.ccMu.Lock()
		s.cc[key] = c
		s.ccMu.Unlock()
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("ETag", c.etag)
	if strings.Contains(r.Header.Get("If-None-Match"), c.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(c.body)
}

// --- sync -----------------------------------------------------------------

type change struct {
	ID      string  `json:"id"`
	Title   *string `json:"title"`
	Done    *bool   `json:"done"`
	Deleted bool    `json:"deleted"`
}

func (s *server) sync(w http.ResponseWriter, r *http.Request, a *account) {
	var in struct {
		Since   string   `json:"since"`
		Changes []change `json:"changes"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	since, _ := strconv.ParseInt(strings.TrimPrefix(in.Since, "v"), 10, 64)
	if len(in.Changes) > 500 {
		labkit.Error(w, 413, "too_many_changes", "send at most 500 changes per sync")
		return
	}
	a.mu.Lock()
	s.load(a)
	applied, created := 0, 0
	for _, c := range in.Changes {
		it := a.byID[c.ID]
		if it == nil {
			if c.Deleted || c.Title == nil {
				continue
			}
			created++
			it = &item{ID: fmt.Sprintf("it_%s_n%d", a.id, s.version.Load()+int64(created))}
			a.items = append(a.items, it)
			a.byID[it.ID] = it
		}
		if c.Title != nil {
			it.Title = *c.Title
		}
		if c.Done != nil {
			it.Done = *c.Done
		}
		it.Deleted = it.Deleted || c.Deleted
		it.Version = s.version.Add(1)
		applied++
	}
	var out []*item
	for _, it := range a.items {
		// Without the fix the token is ignored: everything, every time.
		if !s.cfg.Fixes.On("sync") || it.Version > since {
			cp := *it
			out = append(out, &cp)
		}
	}
	a.mu.Unlock()
	s.serialized.Add(len(out))
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	labkit.JSON(w, 200, map[string]any{"applied": applied, "changes": out, "full": !s.cfg.Fixes.On("sync"), "next": "v" + strconv.FormatInt(s.version.Load(), 10)})
}

// --- analytics ------------------------------------------------------------

func (s *server) ingest(w http.ResponseWriter, r *http.Request, _ *account) {
	var in struct {
		Events []struct {
			Name  string         `json:"name"`
			TS    int64          `json:"ts"`
			Props map[string]any `json:"props"`
		} `json:"events"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	if len(in.Events) == 0 || len(in.Events) > 200 {
		labkit.Error(w, 422, "bad_batch", "send 1 to 200 events")
		return
	}
	for _, e := range in.Events {
		if e.Name == "" {
			labkit.Error(w, 422, "bad_event", "every event needs a name")
			return
		}
	}
	if s.cfg.Fixes.On("events") {
		time.Sleep(2 * s.write) // one write for the batch
		s.writes.Add(1)
		s.emu.Lock()
		s.events += int64(len(in.Events))
		s.emu.Unlock()
	} else {
		// Bottleneck: one write per event, all under the store's one lock.
		s.emu.Lock()
		for range in.Events {
			time.Sleep(s.write)
			s.writes.Add(1)
			s.events++
		}
		s.emu.Unlock()
	}
	labkit.JSON(w, 202, map[string]any{"accepted": len(in.Events)})
}

// --- GraphQL --------------------------------------------------------------

// The schema MobileLab answers (not validated beyond field names):
//
//	type Query { me: User, home: Home, item(id: ID!): Item }
//	type Mutation { toggleItem(id: ID!): Item }
//	type User { id: ID, name: String, streak: Int }
//	type Home { greeting: String, streak: Int, unread: Int, today(limit: Int = 10): [Item], suggestions: [Item] }
//	type Item { id: ID, title: String, done: Boolean, version: Int }
func (s *server) graphql(w http.ResponseWriter, r *http.Request, a *account) {
	var req struct {
		Query         string         `json:"query"`
		OperationName string         `json:"operationName"`
		Variables     map[string]any `json:"variables"`
		Extensions    struct {
			PersistedQuery *struct {
				SHA256 string `json:"sha256Hash"`
			} `json:"persistedQuery"`
		} `json:"extensions"`
	}
	if !labkit.Decode(w, r, &req) {
		return
	}
	fail := func(msg, code string) {
		labkit.JSON(w, 200, map[string]any{"errors": []map[string]any{{"message": msg, "extensions": map[string]string{"code": code}}}})
	}
	if pq := req.Extensions.PersistedQuery; pq != nil {
		s.apqMu.Lock()
		if req.Query == "" {
			req.Query = s.apq[pq.SHA256]
		} else if sum := sha256.Sum256([]byte(req.Query)); hex.EncodeToString(sum[:]) == pq.SHA256 {
			s.apq[pq.SHA256] = req.Query
		}
		s.apqMu.Unlock()
		if req.Query == "" {
			fail("PersistedQueryNotFound", "PERSISTED_QUERY_NOT_FOUND")
			return
		}
	}
	doc, err := parser.ParseQuery(&ast.Source{Input: req.Query})
	if err != nil {
		fail(err.Error(), "GRAPHQL_PARSE_FAILED")
		return
	}
	op := doc.Operations.ForName(req.OperationName)
	if op == nil {
		fail("operation not found", "BAD_REQUEST")
		return
	}
	a.mu.Lock()
	s.load(a)
	data := map[string]any{}
	var errs []map[string]any
	for _, sel := range op.SelectionSet {
		f, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		v, err := s.root(a, op.Operation, f, req.Variables)
		if err != nil {
			errs = append(errs, map[string]any{"message": err.Error(), "path": []string{f.Alias}})
		}
		data[f.Alias] = project(v, f.SelectionSet, req.Variables)
	}
	a.mu.Unlock()
	out := map[string]any{"data": data}
	if len(errs) > 0 {
		out["errors"] = errs
	}
	labkit.JSON(w, 200, out)
}

func itemMap(it *item) map[string]any {
	return map[string]any{"id": it.ID, "title": it.Title, "done": it.Done, "version": it.Version}
}

// root resolves a top-level field; the caller holds a.mu.
func (s *server) root(a *account, op ast.Operation, f *ast.Field, vars map[string]any) (any, error) {
	id := func() string {
		if x := f.Arguments.ForName("id"); x != nil {
			if v, err := x.Value.Value(vars); err == nil && v != nil {
				return fmt.Sprint(v)
			}
		}
		return ""
	}
	if op == ast.Mutation {
		if f.Name != "toggleItem" {
			return nil, fmt.Errorf("unknown mutation %s", f.Name)
		}
		it := a.byID[id()]
		if it == nil || it.Deleted {
			return nil, fmt.Errorf("item %s not found", id())
		}
		it.Done = !it.Done
		it.Version = s.version.Add(1)
		return itemMap(it), nil
	}
	switch f.Name {
	case "me":
		return map[string]any{"id": a.id, "name": a.name, "streak": a.streak}, nil
	case "home":
		var open []any
		for _, it := range a.items {
			if !it.Done && !it.Deleted {
				open = append(open, itemMap(it))
			}
		}
		return map[string]any{
			"greeting": "Good day, " + a.name, "streak": a.streak, "unread": len(a.devices) % 3,
			"today": func(args map[string]any) any {
				limit := 10
				if l, ok := args["limit"].(int64); ok && l > 0 {
					limit = int(l)
				}
				return open[:min(limit, len(open))]
			},
			"suggestions": []any{map[string]any{"id": "sg_1", "title": "Go to bed by 23:00"}, map[string]any{"id": "sg_2", "title": "Two glasses of water before lunch"}},
		}, nil
	case "item":
		if it := a.byID[id()]; it != nil && !it.Deleted {
			return itemMap(it), nil
		}
		return nil, fmt.Errorf("item %s not found", id())
	case "__typename":
		return "Query", nil
	}
	return nil, fmt.Errorf("unknown field %s", f.Name)
}

// project keeps the selected fields of a value; a field may be a function
// of its arguments.
func project(v any, sel ast.SelectionSet, vars map[string]any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = project(e, sel, vars)
		}
		return out
	case map[string]any:
		if len(sel) == 0 {
			return x
		}
		out := map[string]any{}
		for _, s := range sel {
			f, ok := s.(*ast.Field)
			if !ok {
				continue
			}
			val := x[f.Name]
			if fn, ok := val.(func(map[string]any) any); ok {
				args := map[string]any{}
				for _, a := range f.Arguments {
					if av, err := a.Value.Value(vars); err == nil {
						args[a.Name] = av
					}
				}
				val = fn(args)
			}
			out[f.Alias] = project(val, f.SelectionSet, vars)
		}
		return out
	}
	return v
}
