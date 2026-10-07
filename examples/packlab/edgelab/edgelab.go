// Package edgelab is EdgeLab, a small serverless app on a simulated
// function platform: a link shortener whose routes are functions (create
// a link, redirect, stats, a link-preview image) running on instances that
// start cold, stay warm while used, are reclaimed when idle, and are
// limited in number per function. It is the reference app for the
// serverless pack.
//
// Each response says whether it started cold (X-Cold-Start), which
// instance served it, and how long init and execution took
// (Server-Timing). Over a function's concurrency limit the answer is 429
// with Retry-After, as function platforms throttle.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - init: a cold start loads the whole bundle, 400 modules, before the
//     first request (800ms) (fix "init" loads 40 up front and the rest
//     when first needed: 80ms).
//   - prewarm: no instances are kept ready, so every burst of traffic
//     pays cold starts (fix "prewarm" keeps five provisioned instances of
//     each function warm).
//   - pool: every invocation opens its own database connection (20ms)
//     and closes it (fix "pool" opens one per instance at init and reuses
//     it).
package edgelab

import (
	"fmt"
	"hash/fnv"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

const (
	// AccountLimit is the most instances running at once across functions.
	AccountLimit = 200
	modules      = 400
	provisioned  = 5
)

type instance struct {
	id          string
	busy        bool
	lastUsed    time.Time
	provisioned bool
	conn        bool // holds a database connection (pool fix)
}

type function struct {
	name  string
	limit int           // reserved concurrency
	exec  time.Duration // work per invocation
	db    bool          // uses the database

	mu        sync.Mutex
	instances []*instance
	next      int
	cold      int
	throttled int
	invoked   int
}

type link struct {
	Code    string `json:"code"`
	URL     string `json:"url"`
	Created string `json:"createdAt"`
	clicks  int
}

type server struct {
	cfg     labkit.Config
	module  time.Duration // loading one module
	connect time.Duration // opening a database connection
	idleTTL time.Duration
	query   time.Duration

	fns     map[string]*function
	order   []string
	running int // instances across functions
	rmu     sync.Mutex

	lmu   sync.RWMutex
	links map[string]*link
	nextL int

	loaded   labkit.Counter // modules loaded by cold starts
	connects labkit.Counter // database connections opened
	colds    labkit.Counter // cold starts
}

// New returns EdgeLab's handler.
func New(cfg labkit.Config) http.Handler {
	_, h := newServer(cfg)
	return h
}

func newServer(cfg labkit.Config) (*server, http.Handler) {
	s := &server{cfg: cfg, module: 2 * time.Millisecond, connect: 20 * time.Millisecond, idleTTL: time.Minute, query: 2 * time.Millisecond,
		fns: map[string]*function{}, links: map[string]*link{}}
	if cfg.Fast {
		s.module, s.connect, s.idleTTL, s.query = 20*time.Microsecond, 200*time.Microsecond, 2*time.Second, 20*time.Microsecond
	}
	preview := 200 * time.Millisecond
	if cfg.Fast {
		preview = 50 * time.Millisecond // kept long: the concurrency limit is the point
	}
	for _, f := range []*function{
		{name: "home", limit: 50, exec: time.Millisecond},
		{name: "hello", limit: 50, exec: time.Millisecond},
		{name: "create-link", limit: 50, exec: 2 * time.Millisecond, db: true},
		{name: "redirect", limit: 100, exec: time.Millisecond, db: true},
		{name: "link-stats", limit: 50, exec: 2 * time.Millisecond, db: true},
		{name: "preview", limit: 5, exec: preview},
	} {
		if cfg.Fast && f.name != "preview" {
			f.exec /= 10
		}
		s.fns[f.name] = f
		s.order = append(s.order, f.name)
		if cfg.Fixes.On("prewarm") {
			for range min(provisioned, f.limit) {
				f.next++
				f.instances = append(f.instances, &instance{id: fmt.Sprintf("%s-%d", f.name, f.next), provisioned: true,
					lastUsed: time.Now(), conn: cfg.Fixes.On("pool") && f.db})
				s.running++
			}
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i := 1000; i < 2000; i++ {
		code := "go" + strconv.Itoa(i)
		s.links[code] = &link{Code: code, URL: fmt.Sprintf("https://example.com/articles/%d", i), Created: now}
	}
	routes := []labkit.Route{
		{Method: "GET", Path: "/api/hello", Tag: "functions", Summary: "Function hello"},
		{Method: "POST", Path: "/api/links", Tag: "functions", Summary: "Function create-link: shorten a URL"},
		{Method: "GET", Path: "/r/{code}", Tag: "functions", Summary: "Function redirect: 301 to the long URL"},
		{Method: "GET", Path: "/api/links/{code}/stats", Tag: "functions", Summary: "Function link-stats"},
		{Method: "GET", Path: "/api/preview", Tag: "functions", Summary: "Function preview: a link-preview image (?url=); reserved concurrency 5"},
		{Method: "GET", Path: "/_platform/functions", Tag: "serverless", Summary: "The platform's view: instances, cold starts, throttles per function"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.fn("home", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "EdgeLab", "links": map[string]string{"openapi": "/openapi.json"}})
	}))
	mux.Handle("GET /openapi.json", labkit.OpenAPI("EdgeLab", "A link shortener running as functions on a simulated serverless platform, with planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/hello", s.fn("hello", func(w http.ResponseWriter, r *http.Request) {
		labkit.JSON(w, 200, map[string]any{"message": "hello " + r.URL.Query().Get("name"), "region": "lab-1"})
	}))
	mux.HandleFunc("POST /api/links", s.fn("create-link", s.createLink))
	mux.HandleFunc("GET /r/{code}", s.fn("redirect", s.redirect))
	mux.HandleFunc("GET /api/links/{code}/stats", s.fn("link-stats", s.stats))
	mux.HandleFunc("GET /api/preview", s.fn("preview", s.preview))
	mux.HandleFunc("GET /_platform/functions", s.platform)
	return s, mux
}

// acquire finds a warm instance or starts one; nil means throttled.
func (s *server) acquire(f *function) (*instance, bool) {
	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invoked++
	// Reclaim instances idle too long (provisioned ones stay).
	kept := f.instances[:0]
	reaped := 0
	for _, in := range f.instances {
		if !in.busy && !in.provisioned && now.Sub(in.lastUsed) > s.idleTTL {
			reaped++
			continue
		}
		kept = append(kept, in)
	}
	f.instances = kept
	if reaped > 0 {
		s.rmu.Lock()
		s.running -= reaped
		s.rmu.Unlock()
	}
	for i := len(f.instances) - 1; i >= 0; i-- { // most recently used first
		if in := f.instances[i]; !in.busy {
			in.busy = true
			return in, false
		}
	}
	s.rmu.Lock()
	full := len(f.instances) >= f.limit || s.running >= AccountLimit
	if !full {
		s.running++
	}
	s.rmu.Unlock()
	if full {
		f.throttled++
		return nil, false
	}
	f.next++
	f.cold++
	in := &instance{id: fmt.Sprintf("%s-%d", f.name, f.next), busy: true}
	f.instances = append(f.instances, in)
	return in, true
}

func (s *server) release(f *function, in *instance) {
	f.mu.Lock()
	in.busy, in.lastUsed = false, time.Now()
	// Keep the most recently used instances at the end of the list.
	for i, x := range f.instances {
		if x == in {
			f.instances = append(append(f.instances[:i:i], f.instances[i+1:]...), in)
			break
		}
	}
	f.mu.Unlock()
}

// initialize is a cold start: load the bundle and, with the pool fix,
// open the instance's database connection.
func (s *server) initialize(f *function, in *instance) time.Duration {
	start := time.Now()
	s.colds.Add(1)
	n := modules
	if s.cfg.Fixes.On("init") {
		n = modules / 10 // the rest load when first needed
	}
	s.loaded.Add(n)
	time.Sleep(time.Duration(n) * s.module)
	if f.db && s.cfg.Fixes.On("pool") {
		s.dial()
		in.conn = true
	}
	return time.Since(start)
}

func (s *server) dial() {
	s.connects.Add(1)
	time.Sleep(s.connect)
}

// fn runs a handler as a function invocation on the platform.
func (s *server) fn(name string, h http.HandlerFunc) http.HandlerFunc {
	f := s.fns[name]
	return func(w http.ResponseWriter, r *http.Request) {
		in, cold := s.acquire(f)
		if in == nil {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("X-Throttled", "true")
			labkit.Error(w, http.StatusTooManyRequests, "TooManyRequestsException", "rate exceeded: function "+name+" is at its concurrency limit")
			return
		}
		defer s.release(f, in)
		var initDur time.Duration
		if cold {
			initDur = s.initialize(f, in)
		}
		start := time.Now()
		if f.db && !in.conn {
			s.dial() // bottleneck: a new database connection for every invocation
		}
		time.Sleep(f.exec)
		hdr := w.Header()
		hdr.Set("Function-Execution-Id", labkit.Token("")[:16])
		hdr.Set("X-Function-Instance", in.id)
		hdr.Set("X-Cold-Start", strconv.FormatBool(cold))
		hdr.Set("Server-Timing", fmt.Sprintf("init;dur=%.1f, exec;dur=%.1f", float64(initDur.Microseconds())/1000, float64(time.Since(start).Microseconds())/1000))
		h(w, r)
	}
}

// --- the app's functions ----------------------------------------------------

func (s *server) createLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	u, err := url.Parse(in.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		labkit.Error(w, 422, "bad_url", "give an http or https url")
		return
	}
	time.Sleep(s.query)
	s.lmu.Lock()
	s.nextL++
	h := fnv.New32a()
	h.Write([]byte(in.URL))
	code := fmt.Sprintf("n%x%d", h.Sum32()%4096, s.nextL)
	l := &link{Code: code, URL: in.URL, Created: time.Now().UTC().Format(time.RFC3339)}
	s.links[code] = l
	s.lmu.Unlock()
	labkit.JSON(w, 201, map[string]any{"code": code, "short": "/r/" + code, "url": in.URL})
}

func (s *server) redirect(w http.ResponseWriter, r *http.Request) {
	time.Sleep(s.query)
	s.lmu.Lock()
	l := s.links[r.PathValue("code")]
	if l != nil {
		l.clicks++
	}
	s.lmu.Unlock()
	if l == nil {
		labkit.Error(w, 404, "not_found", "no such link")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=0")
	http.Redirect(w, r, l.URL, http.StatusMovedPermanently)
}

func (s *server) stats(w http.ResponseWriter, r *http.Request) {
	time.Sleep(s.query)
	s.lmu.RLock()
	l := s.links[r.PathValue("code")]
	var out map[string]any
	if l != nil {
		out = map[string]any{"code": l.Code, "url": l.URL, "clicks": l.clicks, "createdAt": l.Created}
	}
	s.lmu.RUnlock()
	if out == nil {
		labkit.Error(w, 404, "not_found", "no such link")
		return
	}
	labkit.JSON(w, 200, out)
}

// preview renders a small grey image (a PGM: an image format simple
// enough to write by hand) whose shading depends on the URL.
func (s *server) preview(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("url")
	if u == "" {
		labkit.Error(w, 422, "bad_url", "give ?url=")
		return
	}
	h := fnv.New32a()
	h.Write([]byte(u))
	seed := h.Sum32()
	const wd, ht = 120, 63
	var b strings.Builder
	fmt.Fprintf(&b, "P5\n%d %d\n255\n", wd, ht)
	for y := range ht {
		for x := range wd {
			b.WriteByte(byte((x*int(seed%7+1) + y*3 + int(seed>>8)) % 256))
		}
	}
	w.Header().Set("Content-Type", "image/x-portable-graymap")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write([]byte(b.String()))
}

func (s *server) platform(w http.ResponseWriter, _ *http.Request) {
	var out []map[string]any
	for _, name := range s.order {
		f := s.fns[name]
		f.mu.Lock()
		busy, warm := 0, 0
		for _, in := range f.instances {
			if in.busy {
				busy++
			} else {
				warm++
			}
		}
		out = append(out, map[string]any{"name": name, "reservedConcurrency": f.limit, "instances": len(f.instances), "busy": busy, "warm": warm,
			"invocations": f.invoked, "coldStarts": f.cold, "throttles": f.throttled})
		f.mu.Unlock()
	}
	s.rmu.Lock()
	running := s.running
	s.rmu.Unlock()
	labkit.JSON(w, 200, map[string]any{"functions": out, "running": running, "accountLimit": AccountLimit})
}
