// Package sociallab is SocialLab, a social network's API: a home feed
// built from the accounts a user follows, posts, likes, comments,
// follows, and notifications delivered live over a WebSocket. It is the
// reference app for the social pack.
//
// Twenty celebrity accounts are followed by everyone, which is where a
// viral spike lands.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - timeline: the home feed is built on every read by gathering every
//     post of every followed account and sorting them all (fix "timeline"
//     keeps a per-user timeline filled as posts are written, and merges in
//     only the newest celebrity posts when it is read).
//   - likes: every like takes one global lock, checks for a repeat like by
//     scanning the post's list of likers and writes the like row (2ms)
//     before letting go, so likes run one at a time across the whole app
//     and a viral post's likes slow down as they pile up (fix "likes"
//     keeps likers in a set under the post's own lock and writes the row
//     outside it).
//   - notify: each notification is stored (2ms) and written to the
//     recipient's open sockets inside the like, comment or follow request,
//     under one notification lock, so a burst of likes queues up behind it
//     and a celebrity's sockets slow every like down (fix "notify" hands
//     notifications to a queue whose worker stores them and passes them
//     to each socket's own writer).
package sociallab

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

const (
	// Users is how many accounts SocialLab has (user0001 ... user5000).
	Users = 5000
	// Celebrities are user0001 ... user0020, followed by everyone.
	Celebrities = 20
	// Password is every account's password.
	Password = "sociallab-pass"

	inboxKeep  = 500
	notesKeep  = 50
	pageSize   = 20
	celebPosts = 300
)

type comment struct {
	ID     int64  `json:"id"`
	Author string `json:"author"`
	Text   string `json:"text"`
	At     string `json:"createdAt"`
}

type post struct {
	id     int64
	author *user
	text   string
	at     time.Time

	mu       sync.Mutex // with the likes fix; also guards comments
	likers   []int32    // without the likes fix
	likerSet map[int32]struct{}
	likes    atomic.Int64
	comments []comment
}

type user struct {
	idx       int32
	name      string
	display   string
	celebrity bool

	// Guarded by server.mu.
	posts     []*post // oldest first
	following []int32 // sorted
	followers []int32
	inbox     []int64 // timeline of non-celebrity posts, oldest first (timeline fix); nil until first read
}

type note struct {
	Type   string `json:"type"`
	Kind   string `json:"kind"`
	From   string `json:"from"`
	PostID int64  `json:"postId,omitempty"`
	Text   string `json:"text,omitempty"`
	At     string `json:"createdAt"`
}

type sub struct {
	conn *websocket.Conn
	out  chan []byte // with the notify fix
}

type server struct {
	cfg labkit.Config

	mu     sync.RWMutex // the follow graph, posts and timelines
	users  []*user
	byName map[string]*user
	posts  map[int64]*post
	nextID int64

	likeMu sync.Mutex // the one lock every like takes without the likes fix

	nmu   sync.Mutex
	notes map[int32][]note
	subs  map[int32][]*sub

	smu      sync.RWMutex
	sessions map[string]*user

	examined     labkit.Counter // posts looked at to build feeds
	likersScan   labkit.Counter // likers compared to spot a repeat like
	inlineWrites labkit.Counter // socket writes done inside a request
	likeGauge    labkit.Gauge   // likes inside the like critical section
	// dbWrite is the cost of writing one row (a like, a notification).
	dbWrite time.Duration
	noteQ   chan queued
	// slow is extra time spent holding a like lock (tests only).
	slow time.Duration
}

// New returns SocialLab's handler.
func New(cfg labkit.Config) http.Handler {
	_, h := newServer(cfg)
	return h
}

var words = strings.Fields("coffee morning launch weekend city music match travel photo recipe garden code book movie run sunset team news idea concert")

func newServer(cfg labkit.Config) (*server, http.Handler) {
	s := &server{cfg: cfg, byName: map[string]*user{}, posts: map[int64]*post{},
		notes: map[int32][]note{}, subs: map[int32][]*sub{}, sessions: map[string]*user{}, dbWrite: 2 * time.Millisecond}
	if cfg.Fast {
		s.dbWrite = 20 * time.Microsecond
	}
	if cfg.Fixes.On("notify") {
		s.noteQ = make(chan queued, 10000)
		go s.noteWorker()
	}
	s.seed()
	routes := []labkit.Route{
		{Method: "POST", Path: "/api/login", Tag: "session", Summary: "Sign in with username and password; returns a bearer token"},
		{Method: "GET", Path: "/api/feed", Tag: "feed", Summary: "Home feed, newest first (before=<post id> for the next page)"},
		{Method: "POST", Path: "/api/posts", Tag: "posts", Summary: "Publish a post"},
		{Method: "GET", Path: "/api/posts/{id}", Tag: "posts", Summary: "One post with like and comment counts"},
		{Method: "POST", Path: "/api/posts/{id}/like", Tag: "likes", Summary: "Like a post (liking twice is a no-op)"},
		{Method: "DELETE", Path: "/api/posts/{id}/like", Tag: "likes", Summary: "Remove a like"},
		{Method: "GET", Path: "/api/posts/{id}/comments", Tag: "comments", Summary: "Comments, oldest first"},
		{Method: "POST", Path: "/api/posts/{id}/comments", Tag: "comments", Summary: "Comment on a post"},
		{Method: "GET", Path: "/api/users/{username}", Tag: "profiles", Summary: "A profile with follower counts"},
		{Method: "GET", Path: "/api/users/{username}/posts", Tag: "profiles", Summary: "A user's posts, newest first"},
		{Method: "POST", Path: "/api/users/{username}/follow", Tag: "follows", Summary: "Follow a user"},
		{Method: "DELETE", Path: "/api/users/{username}/follow", Tag: "follows", Summary: "Unfollow a user"},
		{Method: "GET", Path: "/api/notifications", Tag: "notifications", Summary: "Recent notifications"},
		{Method: "GET", Path: "/api/notifications/ws", Tag: "notifications", Summary: "WebSocket: notifications as they happen"},
		{Method: "GET", Path: "/api/trending", Tag: "trending", Summary: "Most-liked recent posts"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "SocialLab", "links": map[string]string{"openapi": "/openapi.json"}})
	})
	mux.Handle("GET /openapi.json", labkit.OpenAPI("SocialLab", "A social network: feeds, posts, likes, follows and live notifications, with planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("GET /api/feed", s.auth(s.feed))
	mux.HandleFunc("POST /api/posts", s.auth(s.createPost))
	mux.HandleFunc("GET /api/posts/{id}", s.auth(s.getPost))
	mux.HandleFunc("POST /api/posts/{id}/like", s.auth(s.like))
	mux.HandleFunc("DELETE /api/posts/{id}/like", s.auth(s.unlike))
	mux.HandleFunc("GET /api/posts/{id}/comments", s.auth(s.listComments))
	mux.HandleFunc("POST /api/posts/{id}/comments", s.auth(s.addComment))
	mux.HandleFunc("GET /api/users/{username}", s.auth(s.profile))
	mux.HandleFunc("GET /api/users/{username}/posts", s.auth(s.userPosts))
	mux.HandleFunc("POST /api/users/{username}/follow", s.auth(s.follow))
	mux.HandleFunc("DELETE /api/users/{username}/follow", s.auth(s.unfollow))
	mux.HandleFunc("GET /api/notifications", s.auth(s.listNotes))
	mux.HandleFunc("GET /api/notifications/ws", s.socket)
	mux.HandleFunc("GET /api/trending", s.auth(s.trending))
	return s, mux
}

// seed builds the follow graph and a week of posts: celebrities post a
// lot and are followed by everyone; everyone else follows 50 to 400
// others, and celebrities follow 50.
func (s *server) seed() {
	rng := rand.New(rand.NewPCG(3, 5))
	for i := range Users {
		u := &user{idx: int32(i), name: fmt.Sprintf("user%04d", i+1), celebrity: i < Celebrities}
		u.display = "User " + strconv.Itoa(i+1)
		if u.celebrity {
			u.display = "Celebrity " + strconv.Itoa(i+1)
		}
		s.users = append(s.users, u)
		s.byName[u.name] = u
	}
	for _, u := range s.users {
		set := map[int32]bool{}
		n := 50
		if !u.celebrity {
			for c := range Celebrities {
				set[int32(c)] = true
			}
			n += rng.IntN(351)
		}
		for ; n > 0; n-- {
			if f := int32(Celebrities + rng.IntN(Users-Celebrities)); f != u.idx {
				set[f] = true
			}
		}
		for f := range set {
			u.following = append(u.following, f)
			s.users[f].followers = append(s.users[f].followers, u.idx)
		}
		slices.Sort(u.following)
	}
	normal := 8 * (Users - Celebrities)
	total := normal + celebPosts*Celebrities
	start := time.Now().Add(-7 * 24 * time.Hour)
	step := 7 * 24 * time.Hour / time.Duration(total)
	for k := range total {
		var a *user
		if rng.IntN(total) < celebPosts*Celebrities {
			a = s.users[rng.IntN(Celebrities)]
		} else {
			a = s.users[Celebrities+rng.IntN(Users-Celebrities)]
		}
		s.nextID++
		p := &post{id: s.nextID, author: a, at: start.Add(time.Duration(k) * step),
			text: words[rng.IntN(len(words))] + " " + words[rng.IntN(len(words))] + " #" + words[rng.IntN(len(words))]}
		a.posts = append(a.posts, p)
		s.posts[p.id] = p
	}
}

// --- sessions -------------------------------------------------------------

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	u := s.byName[in.Username]
	if u == nil || in.Password != Password {
		labkit.Error(w, 401, "invalid_credentials", "unknown user or wrong password")
		return
	}
	tok := labkit.Token("sl_")
	s.smu.Lock()
	s.sessions[tok] = u
	s.smu.Unlock()
	labkit.JSON(w, 200, map[string]any{"token": tok, "username": u.name})
}

func (s *server) session(r *http.Request) *user {
	tok := labkit.Bearer(r)
	if tok == "" {
		tok = r.URL.Query().Get("access_token") // WebSockets from browsers cannot set headers
	}
	s.smu.RLock()
	defer s.smu.RUnlock()
	return s.sessions[tok]
}

func (s *server) auth(next func(http.ResponseWriter, *http.Request, *user)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.session(r)
		if u == nil {
			labkit.Error(w, 401, "unauthorized", "sign in at POST /api/login and send Authorization: Bearer <token>")
			return
		}
		next(w, r, u)
	}
}

// --- posts and feeds --------------------------------------------------------

func (s *server) view(p *post) map[string]any {
	p.mu.Lock()
	n := len(p.comments)
	p.mu.Unlock()
	return map[string]any{"id": p.id, "author": p.author.name, "authorName": p.author.display, "text": p.text,
		"createdAt": p.at.UTC().Format(time.RFC3339), "likes": p.likes.Load(), "comments": n}
}

func (s *server) page(w http.ResponseWriter, ps []*post) {
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		out = append(out, s.view(p))
	}
	next := ""
	if len(ps) == pageSize {
		next = strconv.FormatInt(ps[len(ps)-1].id, 10)
	}
	labkit.JSON(w, 200, map[string]any{"posts": out, "next_cursor": next})
}

// newest returns up to n of the user's posts older than before (0: any),
// newest first. The caller holds s.mu.
func newest(u *user, before int64, n int) []*post {
	ps := u.posts
	if before > 0 {
		ps = ps[:sort.Search(len(ps), func(i int) bool { return ps[i].id >= before })]
	}
	out := make([]*post, 0, n)
	for i := len(ps) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, ps[i])
	}
	return out
}

func (s *server) feed(w http.ResponseWriter, r *http.Request, u *user) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	var ps []*post
	if s.cfg.Fixes.On("timeline") {
		ps = s.feedFromTimeline(u, before)
	} else {
		// Bottleneck: gather everything every followed account ever
		// posted and sort it all to show twenty.
		s.mu.RLock()
		var all []*post
		for _, f := range u.following {
			for _, p := range s.users[f].posts {
				if before == 0 || p.id < before {
					all = append(all, p)
				}
			}
		}
		s.mu.RUnlock()
		s.examined.Add(len(all))
		sort.Slice(all, func(i, j int) bool { return all[i].id > all[j].id })
		ps = all[:min(pageSize, len(all))]
	}
	s.page(w, ps)
}

// feedFromTimeline reads the user's timeline (built once, then filled as
// followed accounts post) and merges in the newest celebrity posts.
func (s *server) feedFromTimeline(u *user, before int64) []*post {
	s.mu.RLock()
	built := u.inbox != nil
	s.mu.RUnlock()
	if !built {
		s.mu.Lock()
		if u.inbox == nil {
			var ids []int64
			for _, f := range u.following {
				if fu := s.users[f]; !fu.celebrity {
					for _, p := range fu.posts {
						ids = append(ids, p.id)
					}
				}
			}
			s.examined.Add(len(ids))
			slices.Sort(ids)
			if len(ids) > inboxKeep {
				ids = ids[len(ids)-inboxKeep:]
			}
			u.inbox = append(make([]int64, 0, len(ids)), ids...)
		}
		s.mu.Unlock()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var cand []*post
	for i := len(u.inbox) - 1; i >= 0 && len(cand) < pageSize; i-- {
		if id := u.inbox[i]; before == 0 || id < before {
			cand = append(cand, s.posts[id])
		}
	}
	for _, f := range u.following {
		if fu := s.users[f]; fu.celebrity {
			cand = append(cand, newest(fu, before, pageSize)...)
		}
	}
	s.examined.Add(len(cand))
	sort.Slice(cand, func(i, j int) bool { return cand[i].id > cand[j].id })
	return cand[:min(pageSize, len(cand))]
}

func (s *server) createPost(w http.ResponseWriter, r *http.Request, u *user) {
	var in struct {
		Text string `json:"text"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	if t := strings.TrimSpace(in.Text); t == "" || len(t) > 500 {
		labkit.Error(w, 422, "bad_text", "text is 1 to 500 characters")
		return
	}
	s.mu.Lock()
	s.nextID++
	p := &post{id: s.nextID, author: u, text: in.Text, at: time.Now()}
	u.posts = append(u.posts, p)
	s.posts[p.id] = p
	if s.cfg.Fixes.On("timeline") && !u.celebrity {
		// Fan out on write to followers whose timeline is built.
		for _, f := range u.followers {
			if fu := s.users[f]; fu.inbox != nil {
				fu.inbox = append(fu.inbox, p.id)
				if len(fu.inbox) > 2*inboxKeep {
					fu.inbox = append(fu.inbox[:0:0], fu.inbox[len(fu.inbox)-inboxKeep:]...)
				}
			}
		}
	}
	s.mu.Unlock()
	labkit.JSON(w, 201, s.view(p))
}

func (s *server) post(w http.ResponseWriter, id string) *post {
	n, _ := strconv.ParseInt(id, 10, 64)
	s.mu.RLock()
	p := s.posts[n]
	s.mu.RUnlock()
	if p == nil {
		labkit.Error(w, 404, "not_found", "no such post")
	}
	return p
}

func (s *server) getPost(w http.ResponseWriter, r *http.Request, _ *user) {
	if p := s.post(w, r.PathValue("id")); p != nil {
		labkit.JSON(w, 200, s.view(p))
	}
}

func (s *server) userPosts(w http.ResponseWriter, r *http.Request, _ *user) {
	a := s.byName[r.PathValue("username")]
	if a == nil {
		labkit.Error(w, 404, "not_found", "no such user")
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > pageSize {
		limit = pageSize
	}
	s.mu.RLock()
	ps := newest(a, before, limit)
	s.mu.RUnlock()
	s.page(w, ps)
}

func (s *server) profile(w http.ResponseWriter, r *http.Request, me *user) {
	a := s.byName[r.PathValue("username")]
	if a == nil {
		labkit.Error(w, 404, "not_found", "no such user")
		return
	}
	s.mu.RLock()
	out := map[string]any{"username": a.name, "name": a.display, "celebrity": a.celebrity, "followers": len(a.followers),
		"following": len(a.following), "posts": len(a.posts), "followedByYou": isFollowing(me, a.idx)}
	s.mu.RUnlock()
	labkit.JSON(w, 200, out)
}

func isFollowing(u *user, f int32) bool {
	_, ok := slices.BinarySearch(u.following, f)
	return ok
}

func (s *server) follow(w http.ResponseWriter, r *http.Request, me *user) {
	a := s.byName[r.PathValue("username")]
	if a == nil || a == me {
		labkit.Error(w, 404, "not_found", "no such user (or yourself)")
		return
	}
	s.mu.Lock()
	i, found := slices.BinarySearch(me.following, a.idx)
	if !found {
		me.following = slices.Insert(me.following, i, a.idx)
		a.followers = append(a.followers, me.idx)
		me.inbox = nil // rebuilt on the next read
	}
	s.mu.Unlock()
	if !found {
		s.notify(a, note{Type: "notification", Kind: "follow", From: me.name})
	}
	labkit.JSON(w, 200, map[string]any{"following": true, "username": a.name})
}

func (s *server) unfollow(w http.ResponseWriter, r *http.Request, me *user) {
	a := s.byName[r.PathValue("username")]
	if a == nil {
		labkit.Error(w, 404, "not_found", "no such user")
		return
	}
	s.mu.Lock()
	if i, found := slices.BinarySearch(me.following, a.idx); found {
		me.following = slices.Delete(me.following, i, i+1)
		if j := slices.Index(a.followers, me.idx); j >= 0 {
			a.followers = slices.Delete(a.followers, j, j+1)
		}
		me.inbox = nil
	}
	s.mu.Unlock()
	labkit.JSON(w, 200, map[string]any{"following": false, "username": a.name})
}

// --- likes and comments -----------------------------------------------------

// likeLock takes the lock that guards a post's likers.
func (s *server) likeLock(p *post) func() {
	l := &s.likeMu // bottleneck: one lock for every post's likes
	if s.cfg.Fixes.On("likes") {
		l = &p.mu
	}
	l.Lock()
	done := s.likeGauge.Enter()
	if s.slow > 0 {
		time.Sleep(s.slow)
	}
	return func() { done(); l.Unlock() }
}

// setLike adds or removes a like and reports whether anything changed.
func (s *server) setLike(p *post, u *user, on bool) bool {
	if s.cfg.Fixes.On("likes") {
		if !s.setLikeLocked(p, u, on) {
			return false
		}
		time.Sleep(s.dbWrite) // the like row, written outside any lock
		return true
	}
	unlock := s.likeLock(p)
	defer unlock()
	if !s.setLikeLocked(p, u, on) {
		return false
	}
	time.Sleep(s.dbWrite) // bottleneck: the like row is written under the one lock
	return true
}

// setLikeLocked records the like in memory and reports whether anything
// changed. With the likes fix it takes the post's own lock; without it
// the caller holds the global one.
func (s *server) setLikeLocked(p *post, u *user, on bool) bool {
	if s.cfg.Fixes.On("likes") {
		unlock := s.likeLock(p)
		defer unlock()
		_, had := p.likerSet[u.idx]
		switch {
		case on && !had:
			if p.likerSet == nil {
				p.likerSet = map[int32]struct{}{}
			}
			p.likerSet[u.idx] = struct{}{}
		case !on && had:
			delete(p.likerSet, u.idx)
		default:
			return false
		}
	} else {
		// Bottleneck: find a repeat like by scanning every liker.
		at := -1
		for i, x := range p.likers {
			if x == u.idx {
				at = i
				break
			}
		}
		s.likersScan.Add(len(p.likers))
		switch {
		case on && at < 0:
			p.likers = append(p.likers, u.idx)
		case !on && at >= 0:
			p.likers = slices.Delete(p.likers, at, at+1)
		default:
			return false
		}
	}
	if on {
		p.likes.Add(1)
	} else {
		p.likes.Add(-1)
	}
	return true
}

func (s *server) like(w http.ResponseWriter, r *http.Request, u *user) {
	p := s.post(w, r.PathValue("id"))
	if p == nil {
		return
	}
	if s.setLike(p, u, true) && p.author != u {
		s.notify(p.author, note{Type: "notification", Kind: "like", From: u.name, PostID: p.id})
	}
	labkit.JSON(w, 200, map[string]any{"postId": p.id, "liked": true, "likes": p.likes.Load()})
}

func (s *server) unlike(w http.ResponseWriter, r *http.Request, u *user) {
	p := s.post(w, r.PathValue("id"))
	if p == nil {
		return
	}
	s.setLike(p, u, false)
	labkit.JSON(w, 200, map[string]any{"postId": p.id, "liked": false, "likes": p.likes.Load()})
}

func (s *server) listComments(w http.ResponseWriter, r *http.Request, _ *user) {
	p := s.post(w, r.PathValue("id"))
	if p == nil {
		return
	}
	p.mu.Lock()
	cs := p.comments
	if len(cs) > 50 {
		cs = cs[len(cs)-50:]
	}
	out := append([]comment{}, cs...)
	p.mu.Unlock()
	labkit.JSON(w, 200, map[string]any{"postId": p.id, "comments": out})
}

func (s *server) addComment(w http.ResponseWriter, r *http.Request, u *user) {
	p := s.post(w, r.PathValue("id"))
	if p == nil {
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	if t := strings.TrimSpace(in.Text); t == "" || len(t) > 500 {
		labkit.Error(w, 422, "bad_text", "text is 1 to 500 characters")
		return
	}
	s.mu.Lock()
	s.nextID++
	c := comment{ID: s.nextID, Author: u.name, Text: in.Text, At: time.Now().UTC().Format(time.RFC3339Nano)}
	s.mu.Unlock()
	p.mu.Lock()
	p.comments = append(p.comments, c)
	p.mu.Unlock()
	if p.author != u {
		s.notify(p.author, note{Type: "notification", Kind: "comment", From: u.name, PostID: p.id, Text: in.Text})
	}
	labkit.JSON(w, 201, c)
}

// trending ranks the most-liked of the last 2,000 posts.
func (s *server) trending(w http.ResponseWriter, _ *http.Request, _ *user) {
	s.mu.RLock()
	var recent []*post
	for id := s.nextID; id > 0 && len(recent) < 2000; id-- {
		if p := s.posts[id]; p != nil {
			recent = append(recent, p)
		}
	}
	s.mu.RUnlock()
	sort.SliceStable(recent, func(i, j int) bool { return recent[i].likes.Load() > recent[j].likes.Load() })
	out := make([]map[string]any, 0, 10)
	for _, p := range recent[:min(10, len(recent))] {
		out = append(out, s.view(p))
	}
	labkit.JSON(w, 200, map[string]any{"posts": out})
}

// --- notifications ----------------------------------------------------------

// notify records a notification and pushes it to the recipient's open
// sockets. Without the notify fix this happens inside the request, under
// the notification lock; with it a worker does it from a queue.
func (s *server) notify(to *user, n note) {
	n.At = time.Now().UTC().Format(time.RFC3339Nano)
	if s.cfg.Fixes.On("notify") {
		s.noteQ <- queued{to: to, n: n}
		return
	}
	s.deliver(to, n)
}

type queued struct {
	to *user
	n  note
}

func (s *server) noteWorker() {
	for q := range s.noteQ {
		s.deliver(q.to, q.n)
	}
}

func (s *server) deliver(to *user, n note) {
	b, _ := json.Marshal(n)
	s.nmu.Lock()
	defer s.nmu.Unlock()
	time.Sleep(s.dbWrite) // the notification row
	ns := append(s.notes[to.idx], n)
	if len(ns) > 2*notesKeep {
		ns = append(ns[:0:0], ns[len(ns)-notesKeep:]...)
	}
	s.notes[to.idx] = ns
	for _, sb := range s.subs[to.idx] {
		if sb.out != nil {
			select {
			case sb.out <- b:
			default:
				// Too far behind: drop the socket rather than stall.
				_ = sb.conn.Close(websocket.StatusPolicyViolation, "slow consumer")
			}
			continue
		}
		// Bottleneck: write to the socket here, inside the request that
		// caused the notification, holding the notification lock.
		s.inlineWrites.Add(1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = sb.conn.Write(ctx, websocket.MessageText, b)
		cancel()
	}
}

func (s *server) listNotes(w http.ResponseWriter, _ *http.Request, u *user) {
	s.nmu.Lock()
	ns := s.notes[u.idx]
	out := make([]note, 0, min(len(ns), notesKeep))
	for i := len(ns) - 1; i >= 0 && len(out) < notesKeep; i-- {
		out = append(out, ns[i])
	}
	s.nmu.Unlock()
	labkit.JSON(w, 200, map[string]any{"notifications": out})
}

func (s *server) socket(w http.ResponseWriter, r *http.Request) {
	u := s.session(r)
	if u == nil {
		labkit.Error(w, 401, "unauthorized", "sign in first and send Authorization: Bearer <token>")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(16 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sb := &sub{conn: conn}
	if s.cfg.Fixes.On("notify") {
		sb.out = make(chan []byte, 256)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case b := <-sb.out:
					wctx, c := context.WithTimeout(ctx, 5*time.Second)
					err := conn.Write(wctx, websocket.MessageText, b)
					c()
					if err != nil {
						cancel()
						return
					}
				}
			}
		}()
	}
	write := func(v any) {
		b, _ := json.Marshal(v)
		if sb.out != nil {
			select {
			case sb.out <- b:
			case <-ctx.Done():
			}
			return
		}
		wctx, c := context.WithTimeout(ctx, 5*time.Second)
		_ = conn.Write(wctx, websocket.MessageText, b)
		c()
	}
	s.nmu.Lock()
	unread := len(s.notes[u.idx])
	s.subs[u.idx] = append(s.subs[u.idx], sb)
	s.nmu.Unlock()
	defer func() {
		s.nmu.Lock()
		if i := slices.Index(s.subs[u.idx], sb); i >= 0 {
			s.subs[u.idx] = slices.Delete(s.subs[u.idx], i, i+1)
		}
		s.nmu.Unlock()
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()
	write(map[string]any{"type": "welcome", "user": u.name, "unread": unread})
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var in struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &in) == nil && in.Type == "ping" {
			write(map[string]any{"type": "pong"})
		}
	}
}
