// Package gamelab is GameLab, a game backend: sign-in and a leaderboard
// over HTTP, matchmaking over WebSocket, and a UDP game server that
// players join with the ticket matchmaking gave them and send inputs to,
// answered on the server's tick. It is the reference app for the gaming
// pack.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - matchmaking: every tick, each waiting player is compared with every
//     other to find the closest ratings, while holding the queue lock that
//     joins also need, so the work grows with the square of the queue
//     during a rush (fix "matchmaking" sorts the queue by rating once and
//     groups neighbours).
//   - leaderboard: each score re-sorts the whole leaderboard under its
//     lock and each rank lookup scans it, so score submissions and reads
//     slow down with the number of players (fix "leaderboard" keeps it
//     sorted with binary-search inserts and finds ranks the same way).
package gamelab

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"net"
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

// Modes are the match sizes players can queue for.
var Modes = map[string]int{"duel": 2, "squad": 4}

type player struct {
	name   string
	rating int
}

// ticket is a player waiting in the matchmaking queue.
type ticket struct {
	p     *player
	mode  string
	since time.Time
	// host is the address the player reached GameLab on, so the match
	// can point it at the game server on the same host.
	host string
	out  chan []byte
	done atomic.Bool
}

// game is a match on the UDP game server.
type game struct {
	id      string
	tickets map[string]string // ticket -> player
	joined  map[string]string // player's UDP address -> player
	last    time.Time
}

type input struct {
	addr      net.Addr
	game, seq string
}

type entry struct {
	player string
	score  int
}

type server struct {
	cfg    labkit.Config
	tokens sync.Map // token -> *player
	pc     net.PacketConn
	udp    string // the game server's port
	done   chan struct{}

	// Matchmaking.
	qmu      sync.Mutex
	waiting  map[string][]*ticket
	compared labkit.Counter
	matches  atomic.Int64
	every    time.Duration // matchmaking tick
	widen    float64       // rating window growth per second waited
	botAfter time.Duration

	// The game server.
	gmu     sync.Mutex
	games   map[string]*game
	pending []input
	tick    atomic.Int64
	rate    time.Duration // game server tick

	// The leaderboard, sorted by score (highest first), then name.
	bmu     sync.RWMutex
	entries []*entry
	byName  map[string]*entry
	sorted  labkit.Counter
}

// Open starts GameLab's UDP game server on cfg.Listen and returns the app.
func Open(cfg labkit.Config) (*labkit.App, error) {
	_, app, err := newServer(cfg)
	return app, err
}

// Seeded is how many players the leaderboard starts with: p1 to p100000
// (p1 to p10000 with -fast).
func Seeded(fast bool) int {
	if fast {
		return 10000
	}
	return 100000
}

func newServer(cfg labkit.Config) (*server, *labkit.App, error) {
	s := &server{cfg: cfg, done: make(chan struct{}), waiting: map[string][]*ticket{}, games: map[string]*game{},
		byName: map[string]*entry{}, every: 50 * time.Millisecond, widen: 200, botAfter: 3 * time.Second, rate: 50 * time.Millisecond}
	if cfg.Fast {
		s.every, s.widen, s.botAfter, s.rate = 10*time.Millisecond, 2000, 300*time.Millisecond, 5*time.Millisecond
	}
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // seeded on purpose: every run starts with the same leaderboard
	for i := 1; i <= Seeded(cfg.Fast); i++ {
		e := &entry{player: "p" + strconv.Itoa(i), score: rng.IntN(100000)}
		s.entries = append(s.entries, e)
		s.byName[e.player] = e
	}
	sort.Slice(s.entries, func(i, j int) bool { return less(s.entries[i], s.entries[j]) })

	addr := cfg.Listen
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, nil, err
	}
	s.pc = pc
	_, s.udp, _ = net.SplitHostPort(pc.LocalAddr().String())
	go s.serveUDP()
	go s.loop(s.every, s.matchmake)
	go s.loop(s.rate, s.step)

	routes := []labkit.Route{
		{Method: "POST", Path: "/api/login", Tag: "players", Summary: "Sign in with a player name"},
		{Method: "GET", Path: "/api/matchmaking", Tag: "matchmaking", Summary: "WebSocket: queue for a match, Authorization: Bearer <token>"},
		{Method: "GET", Path: "/api/leaderboard", Tag: "leaderboards", Summary: "Top scores (limit, offset)"},
		{Method: "GET", Path: "/api/leaderboard/{player}", Tag: "leaderboards", Summary: "A player's best score and rank"},
		{Method: "POST", Path: "/api/scores", Tag: "scores", Summary: "Submit a score; the best one counts"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "GameLab", "udp": labkit.Dialable(pc.LocalAddr()),
			"links": map[string]string{"openapi": "/openapi.json", "matchmaking": "/api/matchmaking"}})
	})
	mux.Handle("GET /openapi.json", labkit.OpenAPI("GameLab", "Matchmaking, a leaderboard and a UDP game server, with planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("GET /api/matchmaking", s.matchmaking)
	mux.HandleFunc("GET /api/leaderboard", s.top)
	mux.HandleFunc("GET /api/leaderboard/{player}", s.rank)
	mux.HandleFunc("POST /api/scores", s.score)
	return s, &labkit.App{
		Handler: mux,
		Env:     map[string]string{},
		Close: func() {
			close(s.done)
			_ = pc.Close()
		},
	}, nil
}

func (s *server) loop(every time.Duration, f func(time.Time)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case now := <-t.C:
			f(now)
		case <-s.done:
			return
		}
	}
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Player string `json:"player"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	in.Player = strings.TrimSpace(in.Player)
	if in.Player == "" || len(in.Player) > 64 || strings.ContainsAny(in.Player, " \t\n") {
		labkit.Error(w, 400, "invalid_player", "player must be 1 to 64 characters without spaces")
		return
	}
	p := &player{name: in.Player, rating: rating(in.Player)}
	tok := labkit.Token("game_")
	s.tokens.Store(tok, p)
	labkit.JSON(w, 200, map[string]any{"token": tok, "player": p.name, "rating": p.rating})
}

// rating is a player's skill rating, 1000 to 1999, fixed by their name.
func rating(name string) int {
	h := fnv.New32a()
	h.Write([]byte(name))
	return 1000 + int(h.Sum32()%1000)
}

func (s *server) player(r *http.Request) *player {
	if p, ok := s.tokens.Load(labkit.Bearer(r)); ok {
		return p.(*player)
	}
	return nil
}

// Matchmaking.

func (s *server) matchmaking(w http.ResponseWriter, r *http.Request) {
	p := s.player(r)
	if p == nil {
		labkit.Error(w, 401, "unauthorized", "sign in first and send Authorization: Bearer <token>")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(16 << 10)
	ctx := r.Context()
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	send := func(v any) {
		b, _ := json.Marshal(v)
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = conn.Write(wctx, websocket.MessageText, b)
	}
	var cur *ticket
	defer func() {
		if cur != nil {
			s.cancel(cur)
		}
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var in struct {
			Type string `json:"type"`
			Mode string `json:"mode"`
		}
		if json.Unmarshal(data, &in) != nil {
			send(map[string]string{"type": "error", "error": "messages must be JSON"})
			continue
		}
		switch in.Type {
		case "queue":
			if Modes[in.Mode] == 0 {
				send(map[string]string{"type": "error", "error": "mode must be duel or squad"})
				continue
			}
			if cur != nil && !cur.done.Load() {
				send(map[string]string{"type": "error", "error": "already queued"})
				continue
			}
			t := &ticket{p: p, mode: in.Mode, since: time.Now(), host: host, out: make(chan []byte, 1)}
			cur = t
			pos := s.enqueue(t)
			send(map[string]any{"type": "queued", "mode": in.Mode, "position": pos, "rating": p.rating})
			go func() {
				select {
				case b := <-t.out:
					wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
					defer cancel()
					_ = conn.Write(wctx, websocket.MessageText, b)
				case <-ctx.Done():
				}
			}()
		case "cancel":
			if cur != nil && s.cancel(cur) {
				send(map[string]string{"type": "cancelled"})
			}
			cur = nil
		case "ping":
			send(map[string]string{"type": "pong"})
		default:
			send(map[string]string{"type": "error", "error": "unknown type " + in.Type})
		}
	}
}

func (s *server) enqueue(t *ticket) int {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	s.waiting[t.mode] = append(s.waiting[t.mode], t)
	return len(s.waiting[t.mode])
}

// cancel takes a ticket out of the queue; it reports whether it was
// still waiting.
func (s *server) cancel(t *ticket) bool {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	q := s.waiting[t.mode]
	if i := slices.Index(q, t); i >= 0 {
		s.waiting[t.mode] = slices.Delete(q, i, i+1)
		t.done.Store(true)
		return true
	}
	return false
}

// window is how far apart in rating a ticket may be matched: it widens
// the longer the player waits.
func (s *server) window(t *ticket, now time.Time) int {
	return 100 + int(now.Sub(t.since).Seconds()*s.widen)
}

// matchmake runs on every matchmaking tick: it groups waiting players of
// close rating, and fills the matches of players who have waited too long
// with bots.
func (s *server) matchmake(now time.Time) {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	for mode, q := range s.waiting {
		size := Modes[mode]
		var groups [][]*ticket
		if s.cfg.Fixes.On("matchmaking") {
			groups = s.groupSorted(q, size, now)
		} else {
			groups = s.groupNearest(q, size, now)
		}
		taken := map[*ticket]bool{}
		for _, g := range groups {
			for _, t := range g {
				taken[t] = true
			}
			s.start(mode, g, 0)
		}
		var rest []*ticket
		for _, t := range q {
			switch {
			case taken[t]:
			case now.Sub(t.since) >= s.botAfter:
				s.start(mode, []*ticket{t}, size-1)
			default:
				rest = append(rest, t)
			}
		}
		s.waiting[mode] = rest
	}
}

// groupNearest is the bottleneck: for each waiting player, oldest first,
// it scans the whole queue for the closest ratings.
func (s *server) groupNearest(q []*ticket, size int, now time.Time) [][]*ticket {
	taken := map[*ticket]bool{}
	var groups [][]*ticket
	for _, t := range q {
		if taken[t] {
			continue
		}
		w := s.window(t, now)
		var near []*ticket
		for _, o := range q {
			s.compared.Add(1)
			if o != t && !taken[o] && abs(o.p.rating-t.p.rating) <= w {
				near = append(near, o)
			}
		}
		if len(near) < size-1 {
			continue
		}
		sort.Slice(near, func(i, j int) bool {
			return abs(near[i].p.rating-t.p.rating) < abs(near[j].p.rating-t.p.rating)
		})
		g := append([]*ticket{t}, near[:size-1]...)
		for _, x := range g {
			taken[x] = true
		}
		groups = append(groups, g)
	}
	return groups
}

// groupSorted is the fix: sort by rating once and take neighbours whose
// spread every member's window allows.
func (s *server) groupSorted(q []*ticket, size int, now time.Time) [][]*ticket {
	sorted := slices.Clone(q)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].p.rating < sorted[j].p.rating })
	s.compared.Add(len(sorted))
	var groups [][]*ticket
	for i := 0; i+size <= len(sorted); {
		g := sorted[i : i+size]
		spread := g[size-1].p.rating - g[0].p.rating
		ok := true
		for _, t := range g {
			if spread > s.window(t, now) {
				ok = false
				break
			}
		}
		if !ok {
			i++
			continue
		}
		groups = append(groups, slices.Clone(g))
		i += size
	}
	return groups
}

// start creates a game for a group of tickets (plus bots) and tells each
// player where to play.
func (s *server) start(mode string, g []*ticket, bots int) {
	n := s.matches.Add(1)
	gm := &game{id: "m-" + strconv.FormatInt(n, 10), tickets: map[string]string{}, joined: map[string]string{}, last: time.Now()}
	var names []string
	for _, t := range g {
		names = append(names, t.p.name)
	}
	for i := range bots {
		names = append(names, fmt.Sprintf("bot-%d-%d", n, i+1))
	}
	tickets := make([]string, len(g))
	for i, t := range g {
		tickets[i] = labkit.Token("tk_")
		gm.tickets[tickets[i]] = t.p.name
	}
	s.gmu.Lock()
	s.games[gm.id] = gm
	s.gmu.Unlock()
	for i, t := range g {
		t.done.Store(true)
		b, _ := json.Marshal(map[string]any{"type": "match", "match": gm.id, "mode": mode, "players": names,
			"bots": bots, "server": net.JoinHostPort(t.host, s.udp), "ticket": tickets[i]})
		t.out <- b
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// The game server: text datagrams.
//
//	join <match> <ticket>        -> joined <match> <tick>
//	input <match> <seq> [data]   -> state <match> <seq> <tick>, on the next tick
//	leave <match>                -> bye <match>
//	ping [anything]              -> pong [anything]

func (s *server) serveUDP() {
	buf := make([]byte, 2048)
	for {
		n, addr, err := s.pc.ReadFrom(buf)
		if err != nil {
			return
		}
		f := strings.Fields(string(buf[:n]))
		if len(f) == 0 {
			continue
		}
		switch {
		case f[0] == "ping":
			s.reply(addr, "pong "+strings.Join(f[1:], " "))
		case f[0] == "join" && len(f) == 3:
			s.gmu.Lock()
			gm := s.games[f[1]]
			var who string
			if gm != nil {
				who = gm.tickets[f[2]]
			}
			if who != "" {
				gm.joined[addr.String()] = who
				gm.last = time.Now()
			}
			s.gmu.Unlock()
			if who == "" {
				s.reply(addr, "error "+f[1]+" bad ticket")
				continue
			}
			s.reply(addr, fmt.Sprintf("joined %s %d", f[1], s.tick.Load()))
		case f[0] == "input" && len(f) >= 3:
			s.gmu.Lock()
			gm := s.games[f[1]]
			ok := gm != nil && gm.joined[addr.String()] != ""
			if ok {
				gm.last = time.Now()
				s.pending = append(s.pending, input{addr: addr, game: f[1], seq: f[2]})
			}
			s.gmu.Unlock()
			if !ok {
				s.reply(addr, "error "+f[1]+" not joined")
			}
		case f[0] == "leave" && len(f) == 2:
			s.gmu.Lock()
			if gm := s.games[f[1]]; gm != nil {
				delete(gm.joined, addr.String())
			}
			s.gmu.Unlock()
			s.reply(addr, "bye "+f[1])
		default:
			s.reply(addr, "error unknown command")
		}
	}
}

func (s *server) reply(addr net.Addr, msg string) {
	_, _ = s.pc.WriteTo([]byte(msg), addr)
}

// step is the game server's tick: it answers the inputs that arrived
// since the last one and drops games idle for two minutes.
func (s *server) step(now time.Time) {
	tick := s.tick.Add(1)
	s.gmu.Lock()
	in := s.pending
	s.pending = nil
	for id, gm := range s.games {
		if now.Sub(gm.last) > 2*time.Minute {
			delete(s.games, id)
		}
	}
	s.gmu.Unlock()
	for _, x := range in {
		s.reply(x.addr, fmt.Sprintf("state %s %s %d", x.game, x.seq, tick))
	}
}

// The leaderboard.

func less(a, b *entry) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	return a.player < b.player
}

// position finds an entry's index; the caller holds bmu.
func (s *server) position(e *entry) int {
	if s.cfg.Fixes.On("leaderboard") {
		return sort.Search(len(s.entries), func(i int) bool { return !less(s.entries[i], e) })
	}
	// Bottleneck: a scan from the top.
	for i, x := range s.entries {
		if x == e {
			s.sorted.Add(i + 1)
			return i
		}
	}
	return -1
}

func (s *server) score(w http.ResponseWriter, r *http.Request) {
	p := s.player(r)
	if p == nil {
		labkit.Error(w, 401, "unauthorized", "sign in first and send Authorization: Bearer <token>")
		return
	}
	var in struct {
		Score *int `json:"score"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	if in.Score == nil || *in.Score < 0 {
		labkit.Error(w, 400, "invalid_score", "score must be a number of at least 0")
		return
	}
	fix := s.cfg.Fixes.On("leaderboard")
	s.bmu.Lock()
	e := s.byName[p.name]
	switch {
	case e == nil:
		e = &entry{player: p.name, score: *in.Score}
		s.byName[p.name] = e
		if fix {
			s.entries = slices.Insert(s.entries, s.position(e), e)
		} else {
			s.entries = append(s.entries, e)
		}
	case *in.Score <= e.score:
		// Only the best score counts.
	case fix:
		i := s.position(e)
		s.entries = slices.Delete(s.entries, i, i+1)
		e.score = *in.Score
		s.entries = slices.Insert(s.entries, s.position(e), e)
	default:
		e.score = *in.Score
	}
	if !fix {
		// Bottleneck: re-sort everything after every score.
		sort.Slice(s.entries, func(i, j int) bool { return less(s.entries[i], s.entries[j]) })
		s.sorted.Add(len(s.entries))
	}
	rank, best := s.position(e)+1, e.score
	s.bmu.Unlock()
	labkit.JSON(w, 200, map[string]any{"player": p.name, "score": *in.Score, "best": best, "rank": rank})
}

func (s *server) top(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	s.bmu.RLock()
	defer s.bmu.RUnlock()
	offset = min(max(offset, 0), len(s.entries))
	out := []map[string]any{}
	for i, e := range s.entries[offset:min(offset+limit, len(s.entries))] {
		out = append(out, map[string]any{"rank": offset + i + 1, "player": e.player, "score": e.score})
	}
	labkit.JSON(w, 200, map[string]any{"entries": out, "total": len(s.entries)})
}

func (s *server) rank(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("player")
	s.bmu.RLock()
	e := s.byName[name]
	var rank, score int
	if e != nil {
		rank, score = s.position(e)+1, e.score
	}
	s.bmu.RUnlock()
	if e == nil {
		labkit.Error(w, 404, "not_found", "no score for "+name)
		return
	}
	labkit.JSON(w, 200, map[string]any{"player": name, "score": score, "rank": rank})
}
