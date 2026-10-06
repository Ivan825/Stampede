// Package chatlab is ChatLab, a chat service: rooms, message history over
// HTTP and live messages over WebSocket with fan-out to every member. It is
// the reference app for the chat pack.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - fanout: a message is written to every member in turn while the room
//     lock is held, so delivery time grows with the room's size and one
//     slow reader stalls the room (fix "fanout" queues per connection and
//     drops readers that fall too far behind).
//   - history: the room log is never trimmed, and a history request
//     encodes the whole log to return its tail, so history gets slower
//     with every message ever sent (fix "history" keeps the last 200).
//   - presence: each join and leave sends the full member list to every
//     member, so a reconnect storm costs the square of the room's size
//     (fix "presence" sends only who joined or left and the count).
package chatlab

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

// Rooms are the rooms ChatLab starts with.
var Rooms = []string{"general", "random", "support", "engineering", "sales", "town-hall"}

const keep = 200

type message struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
	Room string `json:"room"`
	User string `json:"user"`
	Text string `json:"text"`
	TS   string `json:"ts"`
}

type member struct {
	user string
	conn *websocket.Conn
	out  chan []byte // with the fanout fix
}

type room struct {
	name    string
	mu      sync.Mutex
	members map[*member]bool
	log     []message
}

type server struct {
	cfg     labkit.Config
	rooms   map[string]*room
	nextID  atomic.Int64
	tokens  sync.Map // token -> user
	encoded labkit.Counter
	// lockedWrites counts socket writes made while holding a room lock.
	lockedWrites labkit.Counter
}

// New returns ChatLab's handler.
func New(cfg labkit.Config) http.Handler {
	_, h := newServer(cfg)
	return h
}

func newServer(cfg labkit.Config) (*server, http.Handler) {
	s := &server{cfg: cfg, rooms: map[string]*room{}}
	for _, n := range Rooms {
		s.rooms[n] = &room{name: n, members: map[*member]bool{}}
	}
	routes := []labkit.Route{
		{Method: "POST", Path: "/api/login", Tag: "sessions", Summary: "Sign in with a display name"},
		{Method: "GET", Path: "/api/rooms", Tag: "rooms", Summary: "List rooms and how many are in each"},
		{Method: "GET", Path: "/api/rooms/{room}/messages", Tag: "messages", Summary: "Recent messages in a room"},
		{Method: "GET", Path: "/ws", Tag: "realtime", Summary: "WebSocket: ?room=<name>, Authorization: Bearer <token>"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "ChatLab", "links": map[string]string{"openapi": "/openapi.json", "websocket": "/ws"}})
	})
	mux.Handle("GET /openapi.json", labkit.OpenAPI("ChatLab", "Chat rooms over WebSocket with planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("GET /api/rooms", s.listRooms)
	mux.HandleFunc("GET /api/rooms/{room}/messages", s.messages)
	mux.HandleFunc("GET /ws", s.ws)
	return s, mux
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		User string `json:"user"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	in.User = strings.TrimSpace(in.User)
	if in.User == "" || len(in.User) > 64 {
		labkit.Error(w, 400, "invalid_user", "user must be 1 to 64 characters")
		return
	}
	tok := labkit.Token("chat_")
	s.tokens.Store(tok, in.User)
	labkit.JSON(w, 200, map[string]string{"token": tok, "user": in.User})
}

func (s *server) user(r *http.Request) (string, bool) {
	tok := labkit.Bearer(r)
	if tok == "" {
		tok = r.URL.Query().Get("token")
	}
	u, ok := s.tokens.Load(tok)
	if !ok {
		return "", false
	}
	return u.(string), true
}

func (s *server) listRooms(w http.ResponseWriter, _ *http.Request) {
	var out []map[string]any
	for _, n := range Rooms {
		rm := s.rooms[n]
		rm.mu.Lock()
		out = append(out, map[string]any{"id": n, "members": len(rm.members), "messages": len(rm.log)})
		rm.mu.Unlock()
	}
	labkit.JSON(w, 200, map[string]any{"rooms": out})
}

// tail returns the last n messages of a room; the caller holds rm.mu.
func (s *server) tail(rm *room, n int) []json.RawMessage {
	out := []json.RawMessage{}
	if s.cfg.Fixes.On("history") {
		for _, m := range rm.log[max(0, len(rm.log)-n):] {
			b, _ := json.Marshal(m)
			out = append(out, b)
		}
		s.encoded.Add(len(out))
		return out
	}
	// Bottleneck: encode the whole log, then keep its tail.
	all := make([]json.RawMessage, 0, len(rm.log))
	for _, m := range rm.log {
		b, _ := json.Marshal(m)
		all = append(all, b)
	}
	s.encoded.Add(len(all))
	return append(out, all[max(0, len(all)-n):]...)
}

func (s *server) messages(w http.ResponseWriter, r *http.Request) {
	rm := s.rooms[r.PathValue("room")]
	if rm == nil {
		labkit.Error(w, 404, "not_found", "no such room")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > keep {
		limit = 50
	}
	rm.mu.Lock()
	msgs := s.tail(rm, limit)
	rm.mu.Unlock()
	labkit.JSON(w, 200, map[string]any{"room": rm.name, "messages": msgs})
}

func (s *server) ws(w http.ResponseWriter, r *http.Request) {
	user, ok := s.user(r)
	if !ok {
		labkit.Error(w, 401, "unauthorized", "sign in first and send Authorization: Bearer <token>")
		return
	}
	rm := s.rooms[r.URL.Query().Get("room")]
	if rm == nil {
		labkit.Error(w, 404, "not_found", "no such room")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(64 << 10)
	m := &member{user: user, conn: conn}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if s.cfg.Fixes.On("fanout") {
		m.out = make(chan []byte, 256)
		go func() {
			for b := range m.out {
				wctx, c := context.WithTimeout(ctx, 5*time.Second)
				err := conn.Write(wctx, websocket.MessageText, b)
				c()
				if err != nil {
					cancel()
					return
				}
			}
		}()
	}

	rm.mu.Lock()
	rm.members[m] = true
	count := len(rm.members)
	rm.mu.Unlock()
	s.send(ctx, m, map[string]any{"type": "welcome", "room": rm.name, "user": user, "members": count})
	s.presence(ctx, rm, user, "joined")
	defer func() {
		rm.mu.Lock()
		delete(rm.members, m)
		rm.mu.Unlock()
		if m.out != nil {
			close(m.out)
		}
		s.presence(context.Background(), rm, user, "left")
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var in struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Limit int    `json:"limit"`
		}
		if json.Unmarshal(data, &in) != nil {
			s.send(ctx, m, map[string]any{"type": "error", "error": "messages must be JSON"})
			continue
		}
		switch in.Type {
		case "ping":
			s.send(ctx, m, map[string]any{"type": "pong"})
		case "typing":
			b, _ := json.Marshal(map[string]any{"type": "typing", "user": user})
			s.broadcast(ctx, rm, b, m)
		case "history":
			if in.Limit <= 0 || in.Limit > keep {
				in.Limit = 50
			}
			rm.mu.Lock()
			msgs := s.tail(rm, in.Limit)
			rm.mu.Unlock()
			s.send(ctx, m, map[string]any{"type": "history", "room": rm.name, "messages": msgs})
		case "say":
			if strings.TrimSpace(in.Text) == "" {
				s.send(ctx, m, map[string]any{"type": "error", "error": "text is required"})
				continue
			}
			msg := message{Type: "message", ID: s.nextID.Add(1), Room: rm.name, User: user, Text: in.Text, TS: time.Now().UTC().Format(time.RFC3339Nano)}
			b, _ := json.Marshal(msg)
			rm.mu.Lock()
			rm.log = append(rm.log, msg)
			if s.cfg.Fixes.On("history") && len(rm.log) > 2*keep {
				rm.log = append(rm.log[:0:0], rm.log[len(rm.log)-keep:]...)
			}
			rm.mu.Unlock()
			s.broadcast(ctx, rm, b, nil)
			s.send(ctx, m, map[string]any{"type": "ack", "id": msg.ID})
		default:
			s.send(ctx, m, map[string]any{"type": "error", "error": "unknown type " + in.Type})
		}
	}
}

func (s *server) send(ctx context.Context, m *member, v any) {
	b, _ := json.Marshal(v)
	s.deliver(ctx, m, b)
}

// deliver writes one message to one member.
func (s *server) deliver(ctx context.Context, m *member, b []byte) {
	if m.out != nil {
		select {
		case m.out <- b:
		default:
			// Too far behind: drop the reader rather than stall the room.
			_ = m.conn.Close(websocket.StatusPolicyViolation, "slow consumer")
		}
		return
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = m.conn.Write(wctx, websocket.MessageText, b)
}

// broadcast sends b to every member of the room except skip.
func (s *server) broadcast(ctx context.Context, rm *room, b []byte, skip *member) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	for m := range rm.members {
		if m != skip {
			// Bottleneck (without "fanout"): a blocking write per member
			// while the room lock is held.
			if m.out == nil {
				s.lockedWrites.Add(1)
			}
			s.deliver(ctx, m, b)
		}
	}
}

func (s *server) presence(ctx context.Context, rm *room, user, event string) {
	var b []byte
	rm.mu.Lock()
	if s.cfg.Fixes.On("presence") {
		b, _ = json.Marshal(map[string]any{"type": "presence", "event": event, "user": user, "count": len(rm.members)})
	} else {
		// Bottleneck: the full member list, to everyone, on every change.
		names := make([]string, 0, len(rm.members))
		for m := range rm.members {
			names = append(names, m.user)
		}
		sort.Strings(names)
		b, _ = json.Marshal(map[string]any{"type": "presence", "event": event, "user": user, "members": names})
	}
	rm.mu.Unlock()
	s.broadcast(ctx, rm, b, nil)
}
