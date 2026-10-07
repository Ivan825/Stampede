// Package newslab is NewsLab, a news site: HTML home, section and article
// pages, an RSS feed and a JSON API, behind a page cache of the kind a CDN
// or reverse proxy provides (Cache-Control, ETag, Age and X-Cache
// headers, conditional requests). Pages are rendered by a small origin
// that can render four at a time. It is the reference app for the content
// pack.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - stampede: when a page is not cached, every request for it renders
//     it, so a popular page that expires or is purged sends a crowd to
//     the origin at once (fix "stampede" lets one request render while the
//     others wait for its result).
//   - purge: publishing an article empties the whole cache, so every
//     update during breaking news makes the site cold (fix "purge" removes
//     only the pages the article appears on).
//   - cachekey: the cache key is the full URL, tracking parameters
//     included, so readers arriving from social links (utm_source,
//     fbclid ...) never hit the cache (fix "cachekey" drops tracking
//     parameters and sorts the rest).
package newslab

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

const (
	// Articles is how many articles NewsLab starts with (story-00001 ...).
	Articles = 20000
	// EditorToken is the bearer token the newsroom publishes with.
	EditorToken = "newslab-editor"

	maxEntries = 20000
)

var sections = []string{"world", "politics", "business", "technology", "sport", "culture", "science", "opinion"}

type article struct {
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	Section   string `json:"section"`
	Summary   string `json:"summary"`
	Author    string `json:"author"`
	Published string `json:"publishedAt"`
	Breaking  bool   `json:"breaking"`
	body      string
	at        time.Time
}

type entry struct {
	body   []byte
	ctype  string
	etag   string
	stored time.Time
	ttl    time.Duration
}

type flight struct {
	done chan struct{}
	e    *entry
}

type server struct {
	cfg  labkit.Config
	cost map[string]time.Duration // render cost by page kind

	amu      sync.RWMutex
	articles []*article // oldest first
	bySlug   map[string]*article
	next     int

	cmu     sync.Mutex
	cache   map[string]*entry
	flights map[string]*flight

	origin chan struct{} // render slots

	hits, misses labkit.Counter
	renders      labkit.Counter
	purged       labkit.Counter
}

// New returns NewsLab's handler.
func New(cfg labkit.Config) http.Handler {
	_, h := newServer(cfg)
	return h
}

var words = strings.Fields("council budget storm market vote talks record city report health energy climate court league final museum festival rail school water tech startup space trial bank housing")

func newServer(cfg labkit.Config) (*server, http.Handler) {
	s := &server{cfg: cfg, bySlug: map[string]*article{}, cache: map[string]*entry{}, flights: map[string]*flight{},
		origin: make(chan struct{}, 4),
		cost: map[string]time.Duration{"home": 100 * time.Millisecond, "section": 60 * time.Millisecond, "article": 40 * time.Millisecond,
			"feed": 50 * time.Millisecond, "list": 20 * time.Millisecond, "json": 10 * time.Millisecond}}
	if cfg.Fast {
		for k, v := range s.cost {
			s.cost[k] = v / 100
		}
	}
	rng := rand.New(rand.NewPCG(9, 1))
	start := time.Now().Add(-30 * 24 * time.Hour)
	for i := 1; i <= Articles; i++ {
		a := s.make(rng, sections[rng.IntN(len(sections))], false)
		a.at = start.Add(time.Duration(i) * 30 * 24 * time.Hour / Articles)
		a.Published = a.at.UTC().Format(time.RFC3339)
		s.add(a)
	}
	routes := []labkit.Route{
		{Method: "GET", Path: "/articles/{slug}", Tag: "articles", Summary: "An article page (HTML)"},
		{Method: "GET", Path: "/section/{name}", Tag: "sections", Summary: "A section front (HTML)"},
		{Method: "GET", Path: "/feed.xml", Tag: "feeds", Summary: "RSS feed of the latest articles"},
		{Method: "GET", Path: "/api/articles", Tag: "articles", Summary: "Latest articles (section, page)"},
		{Method: "GET", Path: "/api/articles/{slug}", Tag: "articles", Summary: "One article"},
		{Method: "POST", Path: "/api/articles", Tag: "newsroom", Summary: "Publish an article (editor token)"},
		{Method: "GET", Path: "/api/sections", Tag: "sections", Summary: "Sections"},
		{Method: "GET", Path: "/api/search", Tag: "search", Summary: "Search headlines (q)"},
		{Method: "GET", Path: "/api/cache/stats", Tag: "cache", Summary: "Cache hits, misses and renders"},
		{Method: "POST", Path: "/api/cache/purge", Tag: "cache", Summary: "Empty the cache (editor token)"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.cached("home", s.renderHome))
	mux.HandleFunc("GET /section/{name}", s.cached("section", s.renderSection))
	mux.HandleFunc("GET /articles/{slug}", s.cached("article", s.renderArticle))
	mux.HandleFunc("GET /feed.xml", s.cached("feed", s.renderFeed))
	mux.HandleFunc("GET /api/articles", s.cached("list", s.renderList))
	mux.HandleFunc("GET /api/articles/{slug}", s.cached("json", s.renderJSON))
	mux.HandleFunc("GET /api/sections", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"sections": sections})
	})
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("POST /api/articles", s.publish)
	mux.HandleFunc("GET /api/cache/stats", s.stats)
	mux.HandleFunc("POST /api/cache/purge", s.purgeAll)
	mux.Handle("GET /openapi.json", labkit.OpenAPI("NewsLab", "A news site behind a page cache, with planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	return s, mux
}

func title(rng *rand.Rand) string {
	w := func() string { return words[rng.IntN(len(words))] }
	t := w() + " " + w() + " " + w() + " " + w()
	return strings.ToUpper(t[:1]) + t[1:]
}

func (s *server) make(rng *rand.Rand, section string, breaking bool) *article {
	s.next++
	t := title(rng)
	return &article{Slug: fmt.Sprintf("story-%05d", s.next), Title: t, Section: section, Breaking: breaking,
		Summary: "What we know about the " + strings.ToLower(t) + ".", Author: fmt.Sprintf("Reporter %d", 1+rng.IntN(60)),
		body: strings.Repeat("The "+strings.ToLower(t)+" was the main story in "+section+" today. ", 12)}
}

func (s *server) add(a *article) {
	s.articles = append(s.articles, a)
	s.bySlug[a.Slug] = a
}

// latest returns up to n articles, newest first, optionally of one
// section, skipping the first skip.
func (s *server) latest(section string, skip, n int) []*article {
	s.amu.RLock()
	defer s.amu.RUnlock()
	var out []*article
	for i := len(s.articles) - 1; i >= 0 && len(out) < n; i-- {
		a := s.articles[i]
		if section != "" && a.Section != section {
			continue
		}
		if skip > 0 {
			skip--
			continue
		}
		out = append(out, a)
	}
	return out
}

// --- the cache --------------------------------------------------------------

var tracking = []string{"utm_", "fbclid", "gclid", "mc_cid", "mc_eid", "ref_src"}

// key returns the cache key for a request.
func (s *server) key(r *http.Request) string {
	if !s.cfg.Fixes.On("cachekey") {
		return r.URL.RequestURI() // bottleneck: tracking parameters split the cache
	}
	q := r.URL.Query()
	for k := range q {
		for _, t := range tracking {
			if strings.HasPrefix(strings.ToLower(k), t) {
				q.Del(k)
			}
		}
	}
	if len(q) == 0 {
		return r.URL.Path
	}
	return r.URL.Path + "?" + q.Encode() // Encode sorts by key
}

type renderFunc func(r *http.Request) (status int, ctype string, body []byte)

// cached serves a page from the cache, rendering it on the origin when
// it is missing or stale.
func (s *server) cached(kind string, render renderFunc) http.HandlerFunc {
	ttl := 60 * time.Second
	if kind == "article" || kind == "json" {
		ttl = 5 * time.Minute
	}
	return func(w http.ResponseWriter, r *http.Request) {
		k := s.key(r)
		now := time.Now()
		s.cmu.Lock()
		e := s.cache[k]
		if e != nil && now.Sub(e.stored) < e.ttl {
			s.cmu.Unlock()
			s.hits.Add(1)
			s.write(w, r, e, "HIT")
			return
		}
		if fl := s.flights[k]; fl != nil {
			// Another request is rendering this page (stampede fix).
			s.cmu.Unlock()
			<-fl.done
			if fl.e != nil {
				s.hits.Add(1)
				s.write(w, r, fl.e, "HIT")
				return
			}
			labkit.Error(w, 404, "not_found", "no such page")
			return
		}
		var fl *flight
		if s.cfg.Fixes.On("stampede") {
			fl = &flight{done: make(chan struct{})}
			s.flights[k] = fl
		}
		s.cmu.Unlock()
		s.misses.Add(1)

		status, ctype, body := s.render(kind, r, render)
		var ne *entry
		if status == 200 {
			sum := sha256.Sum256(body)
			ne = &entry{body: body, ctype: ctype, etag: `"` + hex.EncodeToString(sum[:8]) + `"`, stored: time.Now(), ttl: ttl}
		}
		s.cmu.Lock()
		if ne != nil {
			if len(s.cache) >= maxEntries {
				n := 0
				for old := range s.cache { // make room: drop a tenth, any tenth
					delete(s.cache, old)
					if n++; n >= maxEntries/10 {
						break
					}
				}
			}
			s.cache[k] = ne
		}
		if fl != nil {
			fl.e = ne
			delete(s.flights, k)
			close(fl.done)
		}
		s.cmu.Unlock()
		if ne == nil {
			labkit.Error(w, status, "not_found", "no such page")
			return
		}
		s.write(w, r, ne, "MISS")
	}
}

// render runs a page on the origin, which renders four pages at a time.
func (s *server) render(kind string, r *http.Request, render renderFunc) (int, string, []byte) {
	s.origin <- struct{}{}
	defer func() { <-s.origin }()
	s.renders.Add(1)
	time.Sleep(s.cost[kind]) // templates and database queries
	return render(r)
}

func (s *server) write(w http.ResponseWriter, r *http.Request, e *entry, state string) {
	h := w.Header()
	h.Set("Content-Type", e.ctype)
	h.Set("Cache-Control", fmt.Sprintf("public, max-age=30, s-maxage=%d", int(e.ttl.Seconds())))
	h.Set("ETag", e.etag)
	h.Set("Age", strconv.Itoa(int(time.Since(e.stored).Seconds())))
	h.Set("X-Cache", state)
	if inm := r.Header.Get("If-None-Match"); inm != "" && strings.Contains(inm, e.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(e.body)
}

// --- pages ------------------------------------------------------------------

func page(title, ogType, body string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>%s · NewsLab</title>`, html.EscapeString(title))
	fmt.Fprintf(&b, `<meta property="og:type" content="%s"><meta property="og:site_name" content="NewsLab">`, ogType)
	b.WriteString(`<link rel="alternate" type="application/rss+xml" title="NewsLab" href="/feed.xml"></head><body>`)
	b.WriteString(`<nav>`)
	for _, sec := range sections {
		fmt.Fprintf(&b, `<a class="section" href="/section/%s">%s</a> `, sec, sec)
	}
	b.WriteString(`</nav><main>` + body + `</main></body></html>`)
	return b.Bytes()
}

func teaser(b *bytes.Buffer, a *article) {
	fmt.Fprintf(b, `<article itemscope itemtype="https://schema.org/NewsArticle"><a class="headline" itemprop="url" href="/articles/%s"><span itemprop="headline">%s</span></a><p>%s</p><time itemprop="datePublished">%s</time></article>`,
		a.Slug, html.EscapeString(a.Title), html.EscapeString(a.Summary), a.Published)
}

func (s *server) renderHome(_ *http.Request) (int, string, []byte) {
	list := s.latest("", 0, 30)
	var b bytes.Buffer
	// The top story: the latest breaking story, else the latest article.
	top := list[0]
	for _, a := range list {
		if a.Breaking {
			top = a
			break
		}
	}
	label := "Top story"
	if top.Breaking {
		label = "Breaking"
	}
	fmt.Fprintf(&b, `<section class="lead"><span>%s</span> <a class="top-story" href="/articles/%s">%s</a></section>`, label, top.Slug, html.EscapeString(top.Title))
	for _, a := range list {
		teaser(&b, a)
	}
	return 200, "text/html; charset=utf-8", page("Home", "website", b.String())
}

func (s *server) renderSection(r *http.Request) (int, string, []byte) {
	name := r.PathValue("name")
	if !validSection(name) {
		return 404, "text/plain", []byte("no such section")
	}
	pg, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pg = max(pg, 1)
	var b bytes.Buffer
	fmt.Fprintf(&b, `<h1>%s</h1>`, name)
	for _, a := range s.latest(name, (pg-1)*20, 20) {
		teaser(&b, a)
	}
	fmt.Fprintf(&b, `<a class="next" href="/section/%s?page=%d">More</a>`, name, pg+1)
	return 200, "text/html; charset=utf-8", page(name, "website", b.String())
}

func validSection(name string) bool {
	for _, s := range sections {
		if s == name {
			return true
		}
	}
	return false
}

func (s *server) find(slug string) *article {
	s.amu.RLock()
	defer s.amu.RUnlock()
	return s.bySlug[slug]
}

func (s *server) renderArticle(r *http.Request) (int, string, []byte) {
	a := s.find(r.PathValue("slug"))
	if a == nil {
		return 404, "text/plain", []byte("no such article")
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, `<article itemscope itemtype="https://schema.org/NewsArticle"><h1 class="title" itemprop="headline">%s</h1><p class="byline" itemprop="author">%s</p><time itemprop="datePublished">%s</time><div itemprop="articleBody"><p>%s</p></div></article>`,
		html.EscapeString(a.Title), html.EscapeString(a.Author), a.Published, html.EscapeString(a.body))
	b.WriteString(`<aside><h2>Latest</h2>`)
	for _, x := range s.latest(a.Section, 0, 5) {
		teaser(&b, x)
	}
	b.WriteString(`</aside>`)
	return 200, "text/html; charset=utf-8", page(a.Title, "article", b.String())
}

func (s *server) renderFeed(_ *http.Request) (int, string, []byte) {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>NewsLab</title><link>/</link><description>Latest news</description>`)
	for _, a := range s.latest("", 0, 50) {
		fmt.Fprintf(&b, `<item><title>%s</title><link>/articles/%s</link><guid>%s</guid><pubDate>%s</pubDate><category>%s</category></item>`,
			html.EscapeString(a.Title), a.Slug, a.Slug, a.at.UTC().Format(time.RFC1123Z), a.Section)
	}
	b.WriteString(`</channel></rss>`)
	return 200, "application/rss+xml", b.Bytes()
}

func jsonBody(v any) []byte {
	b, _ := json.Marshal(v)
	return append(b, '\n')
}

func (s *server) renderList(r *http.Request) (int, string, []byte) {
	section := r.URL.Query().Get("section")
	if section != "" && !validSection(section) {
		return 404, "application/json", []byte(`{"error":{"code":"not_found","message":"no such section"}}`)
	}
	pg, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pg = max(pg, 1)
	list := s.latest(section, (pg-1)*20, 20)
	return 200, "application/json", jsonBody(map[string]any{"articles": list, "page": pg})
}

func (s *server) renderJSON(r *http.Request) (int, string, []byte) {
	a := s.find(r.PathValue("slug"))
	if a == nil {
		return 404, "application/json", []byte(`{"error":{"code":"not_found","message":"no such article"}}`)
	}
	return 200, "application/json", jsonBody(map[string]any{"article": a, "body": a.body})
}

// search is never cached: every query reaches the origin's index.
func (s *server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if q == "" {
		labkit.Error(w, 400, "bad_request", "give ?q=")
		return
	}
	s.amu.RLock()
	var out []*article
	for i := len(s.articles) - 1; i >= 0 && len(out) < 20; i-- {
		if strings.Contains(strings.ToLower(s.articles[i].Title), q) {
			out = append(out, s.articles[i])
		}
	}
	s.amu.RUnlock()
	w.Header().Set("Cache-Control", "no-store")
	labkit.JSON(w, 200, map[string]any{"query": q, "results": out})
}

// --- the newsroom -----------------------------------------------------------

func (s *server) publish(w http.ResponseWriter, r *http.Request) {
	if labkit.Bearer(r) != EditorToken {
		labkit.Error(w, 401, "unauthorized", "send Authorization: Bearer <editor token>")
		return
	}
	var in struct {
		Title    string `json:"title"`
		Section  string `json:"section"`
		Summary  string `json:"summary"`
		Body     string `json:"body"`
		Breaking bool   `json:"breaking"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Title) == "" || !validSection(in.Section) {
		labkit.Error(w, 422, "bad_article", "give a title and one of the sections")
		return
	}
	s.amu.Lock()
	s.next++
	now := time.Now()
	a := &article{Slug: fmt.Sprintf("story-%05d", s.next), Title: in.Title, Section: in.Section, Summary: in.Summary,
		Author: "Newsroom", Breaking: in.Breaking, body: in.Body, at: now, Published: now.UTC().Format(time.RFC3339)}
	s.add(a)
	s.amu.Unlock()
	s.purgeFor(a)
	labkit.JSON(w, 201, map[string]any{"slug": a.Slug, "url": "/articles/" + a.Slug, "breaking": a.Breaking})
}

// purgeFor removes the pages a new article appears on.
func (s *server) purgeFor(a *article) {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	if !s.cfg.Fixes.On("purge") {
		s.purged.Add(len(s.cache)) // bottleneck: every page goes
		s.cache = map[string]*entry{}
		return
	}
	for k := range s.cache {
		u, err := url.Parse(k)
		if err != nil {
			continue
		}
		// Article pages' "latest" boxes may stay stale until they expire.
		switch {
		case u.Path == "/", u.Path == "/feed.xml", u.Path == "/section/"+a.Section,
			u.Path == "/api/articles" && (u.Query().Get("section") == "" || u.Query().Get("section") == a.Section):
			delete(s.cache, k)
			s.purged.Add(1)
		}
	}
}

func (s *server) purgeAll(w http.ResponseWriter, r *http.Request) {
	if labkit.Bearer(r) != EditorToken {
		labkit.Error(w, 401, "unauthorized", "send Authorization: Bearer <editor token>")
		return
	}
	s.cmu.Lock()
	n := len(s.cache)
	s.cache = map[string]*entry{}
	s.cmu.Unlock()
	labkit.JSON(w, 200, map[string]any{"purged": n})
}

func (s *server) stats(w http.ResponseWriter, _ *http.Request) {
	s.cmu.Lock()
	n := len(s.cache)
	s.cmu.Unlock()
	h, m := s.hits.Load(), s.misses.Load()
	rate := 0.0
	if h+m > 0 {
		rate = float64(h) / float64(h+m)
	}
	labkit.JSON(w, 200, map[string]any{"hits": h, "misses": m, "hitRate": rate, "renders": s.renders.Load(), "entries": n, "purged": s.purged.Load()})
}
