// Package apilab is APILab, a public developer API: API keys, per-key rate
// limits answered with 429 and Retry-After, cursor pagination, idempotent
// creates and webhook deliveries. It is the reference app for the
// public-apis pack.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - keys: an API key is found by hashing it and scanning every stored
//     key hash, so each request costs more the more keys exist (fix
//     "keys" uses a map).
//   - limiter: rate limits are a sliding-window log behind one global
//     lock that records refused attempts too, so a client that keeps
//     hammering grows its own log and every other key waits on the lock
//     (fix "limiter" uses sharded token buckets).
//   - webhooks: one worker delivers every webhook, so deliveries queue
//     behind each other when events arrive in bursts (fix "webhooks" runs
//     32 workers).
package apilab

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

// Seeded keys: sk_test_00001 to sk_test_20000 on the standard plan, and
// sk_test_free on the free plan.
const Keys = 20000

type plan struct {
	name  string
	rate  float64 // requests per second
	burst float64
}

var (
	standard = plan{"standard", 20, 40}
	free     = plan{"free", 5, 10}
)

type apiKey struct {
	id   string
	hash [32]byte
	plan plan
	used int64
}

type bucket struct {
	tokens float64
	last   time.Time
}

type item struct {
	ID      int     `json:"id"`
	Name    string  `json:"name"`
	Price   float64 `json:"price"`
	Owner   string  `json:"owner,omitempty"`
	Created string  `json:"created"`
}

type delivery struct {
	ID        string `json:"id"`
	Event     string `json:"event"`
	Status    string `json:"status"`
	Attempts  int    `json:"attempts"`
	LatencyMs int64  `json:"latency_ms"`
	queued    time.Time
}

type server struct {
	cfg labkit.Config

	keyList []*apiKey
	keyMap  map[[32]byte]*apiKey
	scanned labkit.Counter

	limMu   sync.Mutex
	windows map[string][]time.Time
	swept   labkit.Counter
	shards  [64]struct {
		sync.Mutex
		m map[string]*bucket
	}

	mu          sync.Mutex
	items       []item
	idempotency map[string]int
	deliveries  map[string]*delivery
	queue       chan *delivery
	delivering  labkit.Gauge
	receiver    time.Duration
}

// New returns APILab's handler.
func New(cfg labkit.Config) http.Handler {
	_, h := newServer(cfg)
	return h
}

func newServer(cfg labkit.Config) (*server, http.Handler) {
	s := &server{cfg: cfg, keyMap: map[[32]byte]*apiKey{}, windows: map[string][]time.Time{},
		idempotency: map[string]int{}, deliveries: map[string]*delivery{},
		queue: make(chan *delivery, 100000), receiver: 20 * time.Millisecond}
	if cfg.Fast {
		s.receiver = time.Millisecond
	}
	for i := range s.shards {
		s.shards[i].m = map[string]*bucket{}
	}
	add := func(id string, p plan) {
		k := &apiKey{id: id, hash: sha256.Sum256([]byte(id)), plan: p}
		s.keyList = append(s.keyList, k)
		s.keyMap[k.hash] = k
	}
	for i := 1; i <= Keys; i++ {
		add(fmt.Sprintf("sk_test_%05d", i), standard)
	}
	add("sk_test_free", free)
	for i := 1; i <= 10000; i++ {
		s.items = append(s.items, item{ID: i, Name: fmt.Sprintf("item %d", i), Price: float64(i%500) + 0.99, Created: "2026-09-01T00:00:00Z"})
	}
	workers := 1
	if cfg.Fixes.On("webhooks") {
		workers = 32
	}
	for range workers {
		go s.deliver()
	}

	routes := []labkit.Route{
		{Method: "GET", Path: "/v1/items", Tag: "items", Summary: "List items (cursor pagination)"},
		{Method: "POST", Path: "/v1/items", Tag: "items", Summary: "Create an item (Idempotency-Key supported)"},
		{Method: "GET", Path: "/v1/items/{id}", Tag: "items", Summary: "Get an item"},
		{Method: "GET", Path: "/v1/usage", Tag: "usage", Summary: "Your plan, rate limit and usage"},
		{Method: "POST", Path: "/v1/webhooks/test", Tag: "webhooks", Summary: "Send a test event to your webhook"},
		{Method: "GET", Path: "/v1/deliveries/{id}", Tag: "webhooks", Summary: "A webhook delivery's status"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(int(standard.rate)))
		labkit.JSON(w, 200, map[string]any{"name": "APILab", "docs": "/openapi.json",
			"auth": "send your key in the X-API-Key header", "plans": map[string]string{"standard": "20 requests/s, burst 40", "free": "5 requests/s, burst 10"}})
	})
	mux.Handle("GET /openapi.json", labkit.OpenAPI("APILab", "Public developer API with API keys, rate limits and webhooks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.Handle("GET /v1/items", s.authed(s.listItems))
	mux.Handle("POST /v1/items", s.authed(s.createItem))
	mux.Handle("GET /v1/items/{id}", s.authed(s.getItem))
	mux.Handle("GET /v1/usage", s.authed(s.usage))
	mux.Handle("POST /v1/webhooks/test", s.authed(s.testWebhook))
	mux.Handle("GET /v1/deliveries/{id}", s.authed(s.getDelivery))
	return s, mux
}

func (s *server) lookup(raw string) *apiKey {
	h := sha256.Sum256([]byte(raw))
	if s.cfg.Fixes.On("keys") {
		return s.keyMap[h]
	}
	// Bottleneck: compare against every stored key hash.
	var found *apiKey
	for _, k := range s.keyList {
		if subtle.ConstantTimeCompare(k.hash[:], h[:]) == 1 {
			found = k
		}
	}
	s.scanned.Add(len(s.keyList))
	return found
}

// allow takes one token from the key's bucket. It returns the tokens
// left and, when refused, how long until one is available.
func (s *server) allow(k *apiKey) (float64, time.Duration, bool) {
	now := time.Now()
	take := func(b *bucket) (float64, time.Duration, bool) {
		b.tokens = math.Min(k.plan.burst, b.tokens+now.Sub(b.last).Seconds()*k.plan.rate)
		b.last = now
		if b.tokens >= 1 {
			b.tokens--
			return b.tokens, 0, true
		}
		wait := time.Duration((1 - b.tokens) / k.plan.rate * float64(time.Second))
		return b.tokens, wait, false
	}
	if s.cfg.Fixes.On("limiter") {
		sh := &s.shards[k.hash[0]%64]
		sh.Lock()
		defer sh.Unlock()
		b := sh.m[k.id]
		if b == nil {
			b = &bucket{tokens: k.plan.burst, last: now}
			sh.m[k.id] = b
		}
		return take(b)
	}
	// Bottleneck: a sliding-window log behind one global lock. Every
	// attempt is logged, refused ones included, and the key's whole log is
	// filtered on each request, so a client that keeps hammering makes its
	// own log, and everyone's wait for the lock, longer.
	s.limMu.Lock()
	defer s.limMu.Unlock()
	log := s.windows[k.id]
	kept := log[:0]
	for _, t := range log {
		if now.Sub(t) < time.Second {
			kept = append(kept, t)
		}
	}
	s.swept.Add(len(log))
	limit := int(k.plan.rate)
	ok := len(kept) < limit
	var wait time.Duration
	if !ok {
		// Until enough of the window's entries expire.
		wait = time.Second - now.Sub(kept[len(kept)-limit])
	}
	s.windows[k.id] = append(kept, now)
	return float64(max(0, limit-len(kept)-1)), wait, ok
}

type keyHandler func(w http.ResponseWriter, r *http.Request, k *apiKey)

func (s *server) authed(h keyHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-API-Key")
		if raw == "" {
			w.Header().Set("WWW-Authenticate", `ApiKey header="X-API-Key"`)
			labkit.Error(w, 401, "missing_api_key", "send your API key in the X-API-Key header")
			return
		}
		k := s.lookup(raw)
		if k == nil {
			w.Header().Set("WWW-Authenticate", `ApiKey header="X-API-Key"`)
			labkit.Error(w, 401, "invalid_api_key", "unknown API key")
			return
		}
		left, wait, ok := s.allow(k)
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(int(k.plan.rate)))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(int(left)))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(wait).Unix(), 10))
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
			labkit.Error(w, 429, "rate_limited", fmt.Sprintf("plan %s allows %.0f requests per second", k.plan.name, k.plan.rate))
			return
		}
		s.mu.Lock()
		k.used++
		s.mu.Unlock()
		h(w, r, k)
	})
}

func (s *server) listItems(w http.ResponseWriter, r *http.Request, _ *apiKey) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	after, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
	s.mu.Lock()
	var page []item
	for i := after; i < len(s.items) && len(page) < limit; i++ {
		page = append(page, s.items[i])
	}
	total := len(s.items)
	s.mu.Unlock()
	next := ""
	if after+len(page) < total {
		next = strconv.Itoa(after + len(page))
	}
	labkit.JSON(w, 200, map[string]any{"data": page, "next_cursor": next, "has_more": next != ""})
}

func (s *server) getItem(w http.ResponseWriter, r *http.Request, _ *apiKey) {
	id, err := strconv.Atoi(r.PathValue("id"))
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil || id < 1 || id > len(s.items) {
		labkit.Error(w, 404, "not_found", "no such item")
		return
	}
	labkit.JSON(w, 200, s.items[id-1])
}

func (s *server) createItem(w http.ResponseWriter, r *http.Request, k *apiKey) {
	var in struct {
		Name  string  `json:"name"`
		Price float64 `json:"price"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || in.Price < 0 {
		labkit.Error(w, 422, "invalid_item", "name is required and price must not be negative")
		return
	}
	idem := r.Header.Get("Idempotency-Key")
	if idem != "" {
		w.Header().Set("Idempotency-Key", idem)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if idem != "" {
		if id, ok := s.idempotency[k.id+"/"+idem]; ok {
			w.Header().Set("Idempotent-Replayed", "true")
			labkit.JSON(w, 201, s.items[id-1])
			return
		}
	}
	it := item{ID: len(s.items) + 1, Name: in.Name, Price: in.Price, Owner: k.id, Created: time.Now().UTC().Format(time.RFC3339)}
	s.items = append(s.items, it)
	if idem != "" {
		s.idempotency[k.id+"/"+idem] = it.ID
	}
	labkit.JSON(w, 201, it)
}

func (s *server) usage(w http.ResponseWriter, _ *http.Request, k *apiKey) {
	s.mu.Lock()
	used := k.used
	s.mu.Unlock()
	labkit.JSON(w, 200, map[string]any{"plan": k.plan.name, "rate_limit_per_second": k.plan.rate, "burst": k.plan.burst, "requests": used})
}

func (s *server) testWebhook(w http.ResponseWriter, _ *http.Request, _ *apiKey) {
	d := &delivery{ID: labkit.Token("dlv_")[:20], Event: "ping", Status: "pending", queued: time.Now()}
	s.mu.Lock()
	s.deliveries[d.ID] = d
	s.mu.Unlock()
	select {
	case s.queue <- d:
	default:
		labkit.Error(w, 503, "queue_full", "too many deliveries waiting")
		return
	}
	labkit.JSON(w, 202, map[string]any{"id": d.ID, "status": "pending"})
}

func (s *server) getDelivery(w http.ResponseWriter, r *http.Request, _ *apiKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.deliveries[r.PathValue("id")]
	if d == nil {
		labkit.Error(w, 404, "not_found", "no such delivery")
		return
	}
	labkit.JSON(w, 200, d)
}

// deliver sends queued webhooks to the (simulated) receiver.
func (s *server) deliver() {
	for d := range s.queue {
		done := s.delivering.Enter()
		time.Sleep(s.receiver)
		done()
		s.mu.Lock()
		d.Attempts++
		d.Status = "delivered"
		d.LatencyMs = time.Since(d.queued).Milliseconds()
		s.mu.Unlock()
	}
}
