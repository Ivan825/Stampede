// Package labkit holds the small pieces every PackLab reference app shares:
// JSON helpers, fix flags, bearer tokens, a minimal OpenAPI document, a
// work counter used to show where the planted bottlenecks spend their
// time, and the App shape of the apps that also listen on another protocol.
package labkit

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
)

// Fixes says which planted bottlenecks are switched off. The zero value
// keeps every bottleneck in place.
type Fixes map[string]bool

// ParseFixes reads a comma-separated list such as "lock,index" or "all".
func ParseFixes(s string) Fixes {
	f := Fixes{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			f[p] = true
		}
	}
	return f
}

// On reports whether the fix for a bottleneck is switched on.
func (f Fixes) On(name string) bool { return f["all"] || f[name] }

// Config is what every reference app is built from.
type Config struct {
	// Fixes switches planted bottlenecks off.
	Fixes Fixes
	// Fast shortens deliberate waits (token pacing, admission ticks) so
	// tests run quickly. Bottlenecks stay in place.
	Fast bool
	// Listen is the address of an app's second listener (its MQTT broker,
	// Kafka cluster, Redis server or UDP game server), for the apps that
	// have one. Tests use "127.0.0.1:0".
	Listen string
	// Postgres is the PostgreSQL connection string DBLab seeds its tables
	// into.
	Postgres string
}

// App is a reference app that serves more than HTTP: a broker, a cache or
// a UDP server next to its HTTP API.
type App struct {
	// Handler serves the HTTP side: the API, /healthz and /openapi.json.
	Handler http.Handler
	// Env holds what the pack's scenarios need besides TARGET_URL to
	// reach the other listeners, such as MQTT_BROKER=tcp://127.0.0.1:8111.
	Env map[string]string
	// Close stops the other listeners.
	Close func()
}

// Dialable returns a listener's address as a client on this machine can
// dial it: an unspecified host (":8111", "[::]:8111") becomes localhost.
func Dialable(addr net.Addr) string {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}

// JSON writes v with a status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Error writes {"error": {"code": code, "message": msg}}.
func Error(w http.ResponseWriter, status int, code, msg string) {
	JSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

// Decode reads a JSON body into v, answering 400 itself on failure.
func Decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// Token returns a random hex token with a prefix.
func Token(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// Bearer returns the token from an Authorization: Bearer header.
func Bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if t, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(t)
	}
	return ""
}

// Route is one documented endpoint.
type Route struct {
	Method, Path, Tag, Summary string
}

// OpenAPI serves a minimal OpenAPI 3.1 document listing the routes. It is
// what `stampede init` reads to recognise the product.
func OpenAPI(title, description string, routes []Route) http.HandlerFunc {
	paths := map[string]map[string]any{}
	tagSet := map[string]bool{}
	for _, r := range routes {
		if paths[r.Path] == nil {
			paths[r.Path] = map[string]any{}
		}
		paths[r.Path][strings.ToLower(r.Method)] = map[string]any{
			"tags":      []string{r.Tag},
			"summary":   r.Summary,
			"responses": map[string]any{"200": map[string]string{"description": "OK"}},
		}
		tagSet[r.Tag] = true
	}
	var tags []map[string]string
	for t := range tagSet {
		tags = append(tags, map[string]string{"name": t})
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i]["name"] < tags[j]["name"] })
	doc := map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]string{"title": title, "version": "1.0.0", "description": description},
		"tags":    tags,
		"paths":   paths,
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}
}

// Counter counts units of work so tests and /debug/work can show what a
// bottleneck costs.
type Counter struct{ n atomic.Int64 }

// Add records n units of work.
func (c *Counter) Add(n int) { c.n.Add(int64(n)) }

// Load returns the total.
func (c *Counter) Load() int64 { return c.n.Load() }

// Gauge tracks how many things are in progress and the most there ever
// were at once.
type Gauge struct{ cur, peak atomic.Int64 }

// Enter starts one; call the returned function when it ends.
func (g *Gauge) Enter() func() {
	n := g.cur.Add(1)
	for {
		p := g.peak.Load()
		if n <= p || g.peak.CompareAndSwap(p, n) {
			break
		}
	}
	return func() { g.cur.Add(-1) }
}

// Peak returns the highest concurrency seen.
func (g *Gauge) Peak() int64 { return g.peak.Load() }
