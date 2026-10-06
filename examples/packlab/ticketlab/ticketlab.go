// Package ticketlab is TicketLab, a ticketing service: events with seat
// maps, seat holds that expire, orders, and a WebSocket waiting room in
// front of the headline on-sale. It is the reference app for the ticketing
// pack.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - lock: one lock guards every event's seats, so a rush on one event
//     blocks holds and orders for all the others (fix "lock" gives each
//     event its own).
//   - seatmap: holds are never pruned, and the seat map and the expiry
//     sweep replay every hold ever made, so they slow down as an on-sale
//     goes on (fix "seatmap" keeps seat states up to date and prunes).
//   - queue: every waiting-room connection finds its place by scanning
//     the whole queue on every tick, so the waiting room costs the square
//     of its length (fix "queue" remembers each place).
package ticketlab

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

const (
	free uint8 = iota
	held
	sold
)

const holdTTL = 2 * time.Minute

type hold struct {
	id      string
	session string
	seats   []int
	expires time.Time
	state   uint8 // held or sold; expired holds become free
}

type event struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Venue    string `json:"venue"`
	Date     string `json:"date"`
	Capacity int    `json:"capacity"`
	Queue    bool   `json:"waitingRoom"`

	perRow  int
	mu      sync.Mutex // with the lock fix
	status  []uint8
	all     []*hold // every hold ever made (without the seatmap fix)
	active  []*hold // live holds (with the seatmap fix)
	soldBy  map[int]string
	over    bool
	soldN   int
	waiting *waitingRoom
}

type waitingRoom struct {
	mu       sync.Mutex
	queue    []string
	index    map[string]int
	admitted int
	credit   float64
	last     time.Time
}

type server struct {
	cfg        labkit.Config
	global     sync.Mutex
	events     []*event
	tick       time.Duration
	rate       float64 // admissions per second
	replayed   labkit.Counter
	scanned    labkit.Counter
	queueScans labkit.Counter
	inflight   labkit.Gauge
	// slow is extra time spent holding an event lock (tests only).
	slow time.Duration
}

// New returns TicketLab's handler.
func New(cfg labkit.Config) http.Handler {
	_, h := newServer(cfg)
	return h
}

func newServer(cfg labkit.Config) (*server, http.Handler) {
	s := &server{cfg: cfg, tick: 200 * time.Millisecond, rate: 50}
	if cfg.Fast {
		s.tick, s.rate = 10*time.Millisecond, 5000
	}
	names := []string{"Headliner: Stadium Night", "Small Club Show", "Jazz at the Park", "City Marathon Expo",
		"Comedy Late Show", "Symphony No. 9", "Indie Weekender", "Football: Derby Day", "Theatre: Hamlet", "Tech Conference"}
	for i := 1; i <= 20; i++ {
		e := &event{ID: i, Name: names[(i-1)%len(names)], Venue: fmt.Sprintf("Venue %d", (i-1)%5+1),
			Date: time.Date(2026, 12, i, 20, 0, 0, 0, time.UTC).Format(time.RFC3339), Capacity: 2000, soldBy: map[int]string{}}
		switch i {
		case 1:
			e.Capacity, e.Queue = 5000, true
			e.waiting = &waitingRoom{index: map[string]int{}, last: time.Now()}
		case 2:
			e.Capacity = 200
		}
		e.perRow = e.Capacity / 20
		e.status = make([]uint8, e.Capacity)
		s.events = append(s.events, e)
	}
	routes := []labkit.Route{
		{Method: "GET", Path: "/api/events", Tag: "events", Summary: "List events"},
		{Method: "GET", Path: "/api/events/{id}", Tag: "events", Summary: "An event with sold and available counts"},
		{Method: "GET", Path: "/api/events/{id}/seats", Tag: "seats", Summary: "The seat map"},
		{Method: "POST", Path: "/api/holds", Tag: "holds", Summary: "Hold seats: best available (quantity) or named seats"},
		{Method: "POST", Path: "/api/orders", Tag: "orders", Summary: "Buy the seats this session holds"},
		{Method: "POST", Path: "/api/queue/{id}/join", Tag: "waiting-room", Summary: "Join an event's waiting room"},
		{Method: "GET", Path: "/ws/queue", Tag: "waiting-room", Summary: "WebSocket: ?event=<id>; position updates until admitted"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "TicketLab", "links": map[string]string{"openapi": "/openapi.json"}})
	})
	mux.Handle("GET /openapi.json", labkit.OpenAPI("TicketLab", "Ticketing with seat holds and a waiting room, with planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/events", s.listEvents)
	mux.HandleFunc("GET /api/events/{id}", s.getEvent)
	mux.HandleFunc("GET /api/events/{id}/seats", s.seatMap)
	mux.HandleFunc("POST /api/holds", s.createHold)
	mux.HandleFunc("POST /api/orders", s.createOrder)
	mux.HandleFunc("POST /api/queue/{id}/join", s.joinQueue)
	mux.HandleFunc("GET /ws/queue", s.queueSocket)
	return s, session(mux)
}

// session gives every visitor a session cookie.
type sidKey struct{}

func session(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := ""
		if c, err := r.Cookie("sid"); err == nil {
			id = c.Value
		}
		if id == "" {
			id = labkit.Token("s_")
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: id, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode}) //nolint:gosec // Secure only under TLS: the lab usually serves plain HTTP on localhost
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sidKey{}, id)))
	})
}

func sid(r *http.Request) string {
	id, _ := r.Context().Value(sidKey{}).(string)
	return id
}

// lock takes the lock that guards an event's seats.
func (s *server) lock(e *event) func() {
	l := &s.global // bottleneck: one lock for every event
	if s.cfg.Fixes.On("lock") {
		l = &e.mu
	}
	l.Lock()
	done := s.inflight.Enter()
	if s.slow > 0 {
		time.Sleep(s.slow)
	}
	return func() { done(); l.Unlock() }
}

func (s *server) event(w http.ResponseWriter, id string) *event {
	n, err := strconv.Atoi(id)
	if err != nil || n < 1 || n > len(s.events) {
		labkit.Error(w, 404, "not_found", "no such event")
		return nil
	}
	return s.events[n-1]
}

// expire frees seats of lapsed holds; the caller holds the event's lock.
func (s *server) expire(e *event, now time.Time) {
	list := e.active
	if !s.cfg.Fixes.On("seatmap") {
		list = e.all // bottleneck: walk every hold ever made
	}
	s.replayed.Add(len(list))
	kept := e.active[:0]
	for _, h := range list {
		if h.state == held && now.After(h.expires) {
			h.state = free
			for _, i := range h.seats {
				if e.status[i] == held {
					e.status[i] = free
				}
			}
		}
		if s.cfg.Fixes.On("seatmap") && h.state == held {
			kept = append(kept, h)
		}
	}
	if s.cfg.Fixes.On("seatmap") {
		e.active = kept
	}
}

func (s *server) counts(e *event) (int, int) {
	var avail, h int
	for _, st := range e.status {
		switch st {
		case free:
			avail++
		case held:
			h++
		}
	}
	return avail, h
}

func (s *server) listEvents(w http.ResponseWriter, _ *http.Request) {
	out := make([]map[string]any, 0, len(s.events))
	for _, e := range s.events {
		unlock := s.lock(e)
		avail, _ := s.counts(e)
		unlock()
		out = append(out, map[string]any{"id": e.ID, "name": e.Name, "venue": e.Venue, "date": e.Date,
			"capacity": e.Capacity, "available": avail, "waitingRoom": e.Queue})
	}
	labkit.JSON(w, 200, map[string]any{"events": out})
}

func (s *server) getEvent(w http.ResponseWriter, r *http.Request) {
	e := s.event(w, r.PathValue("id"))
	if e == nil {
		return
	}
	unlock := s.lock(e)
	s.expire(e, time.Now())
	avail, h := s.counts(e)
	out := map[string]any{"id": e.ID, "name": e.Name, "venue": e.Venue, "date": e.Date, "capacity": e.Capacity,
		"waitingRoom": e.Queue, "available": avail, "held": h, "sold": e.soldN, "oversold": e.over}
	unlock()
	labkit.JSON(w, 200, out)
}

func (s *server) seatID(e *event, i int) string {
	return fmt.Sprintf("%c%d", 'A'+i/e.perRow, i%e.perRow+1)
}

func (s *server) seatIndex(e *event, id string) (int, bool) {
	if len(id) < 2 || id[0] < 'A' || id[0] >= 'A'+20 {
		return 0, false
	}
	n, err := strconv.Atoi(id[1:])
	if err != nil || n < 1 || n > e.perRow {
		return 0, false
	}
	return int(id[0]-'A')*e.perRow + n - 1, true
}

func (s *server) seatMap(w http.ResponseWriter, r *http.Request) {
	e := s.event(w, r.PathValue("id"))
	if e == nil {
		return
	}
	unlock := s.lock(e)
	now := time.Now()
	s.expire(e, now)
	st := make([]uint8, len(e.status))
	if s.cfg.Fixes.On("seatmap") {
		copy(st, e.status)
	} else {
		// Bottleneck: rebuild the map by replaying every hold.
		for _, h := range e.all {
			for _, i := range h.seats {
				if h.state == sold || (h.state == held && now.Before(h.expires)) {
					st[i] = h.state
				}
			}
		}
		s.replayed.Add(len(e.all))
	}
	unlock()
	rows := make([]map[string]string, 20)
	letters := map[uint8]byte{free: 'F', held: 'H', sold: 'S'}
	avail := 0
	for row := range rows {
		var b strings.Builder
		for k := 0; k < e.perRow; k++ {
			x := st[row*e.perRow+k]
			if x == free {
				avail++
			}
			b.WriteByte(letters[x])
		}
		rows[row] = map[string]string{"row": string(rune('A' + row)), "seats": b.String()}
	}
	labkit.JSON(w, 200, map[string]any{"event": e.ID, "available": avail, "legend": "F free, H held, S sold", "rows": rows})
}

// admitted reports whether a session may buy for a waiting-room event.
func (s *server) admitted(e *event, session string) bool {
	if !e.Queue {
		return true
	}
	pos, _ := s.position(e, session)
	return pos == 0
}

func (s *server) createHold(w http.ResponseWriter, r *http.Request) {
	var in struct {
		EventID  int      `json:"eventId"`
		Quantity int      `json:"quantity"`
		Seats    []string `json:"seats"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	e := s.event(w, strconv.Itoa(in.EventID))
	if e == nil {
		return
	}
	sess := sid(r)
	if !s.admitted(e, sess) {
		labkit.Error(w, 403, "waiting_room", "join the waiting room and wait to be admitted")
		return
	}
	if (in.Quantity <= 0) == (len(in.Seats) == 0) || in.Quantity > 8 || len(in.Seats) > 8 {
		labkit.Error(w, 400, "bad_request", "give either quantity (1-8) or seats (up to 8)")
		return
	}
	unlock := s.lock(e)
	defer unlock()
	now := time.Now()
	s.expire(e, now)
	var picked []int
	if len(in.Seats) > 0 {
		for _, id := range in.Seats {
			i, ok := s.seatIndex(e, id)
			if !ok {
				labkit.Error(w, 400, "bad_seat", "no seat "+id)
				return
			}
			if e.status[i] != free {
				labkit.Error(w, 409, "seat_taken", "seat "+id+" is not available")
				return
			}
			picked = append(picked, i)
		}
	} else {
		for i, st := range e.status {
			if st == free {
				picked = append(picked, i)
				if len(picked) == in.Quantity {
					break
				}
			}
		}
		if len(picked) < in.Quantity {
			labkit.Error(w, 409, "sold_out", "not enough seats left")
			return
		}
	}
	h := &hold{id: labkit.Token("h_")[:18], session: sess, seats: picked, expires: now.Add(holdTTL), state: held}
	for _, i := range picked {
		e.status[i] = held
	}
	e.all = append(e.all, h)
	if s.cfg.Fixes.On("seatmap") {
		e.active = append(e.active, h)
	}
	ids := make([]string, len(picked))
	for k, i := range picked {
		ids[k] = s.seatID(e, i)
	}
	labkit.JSON(w, 201, map[string]any{"holdId": h.id, "eventId": e.ID, "seats": ids, "expiresAt": h.expires.UTC().Format(time.RFC3339)})
}

func (s *server) createOrder(w http.ResponseWriter, r *http.Request) {
	sess := sid(r)
	now := time.Now()
	var seats []string
	var eventID int
	for _, e := range s.events {
		unlock := s.lock(e)
		list := e.active
		if !s.cfg.Fixes.On("seatmap") {
			list = e.all
		}
		s.scanned.Add(len(list))
		for _, h := range list {
			if h.session != sess || h.state != held || now.After(h.expires) {
				continue
			}
			h.state = sold
			for _, i := range h.seats {
				if prev, dup := e.soldBy[i]; dup && prev != h.id {
					e.over = true
				}
				e.soldBy[i] = h.id
				e.status[i] = sold
				e.soldN++
				seats = append(seats, s.seatID(e, i))
			}
			eventID = e.ID
		}
		unlock()
	}
	if len(seats) == 0 {
		labkit.Error(w, 409, "no_active_hold", "this session holds no seats (expired or never held)")
		return
	}
	labkit.JSON(w, 201, map[string]any{"orderId": labkit.Token("o_")[:18], "eventId": eventID, "seats": seats})
}

// position returns how many sessions are ahead in the queue (0 when
// admitted) and whether the session is queued at all.
func (s *server) position(e *event, session string) (int, bool) {
	q := e.waiting
	q.mu.Lock()
	defer q.mu.Unlock()
	// Admit people at the configured rate.
	now := time.Now()
	q.credit += now.Sub(q.last).Seconds() * s.rate
	q.last = now
	if n := int(q.credit); n > 0 {
		q.admitted = min(len(q.queue), q.admitted+n)
		q.credit -= float64(n)
	}
	if q.admitted == len(q.queue) {
		q.credit = 0 // no banking admissions while nobody waits
	}
	idx, ok := -1, false
	if s.cfg.Fixes.On("queue") {
		idx, ok = q.index[session]
	} else {
		// Bottleneck: find our place by scanning the queue.
		for i, x := range q.queue {
			if x == session {
				idx, ok = i, true
			}
		}
		s.queueScans.Add(len(q.queue))
	}
	if !ok {
		return -1, false
	}
	return max(0, idx-q.admitted+1), true
}

func (s *server) joinQueue(w http.ResponseWriter, r *http.Request) {
	e := s.event(w, r.PathValue("id"))
	if e == nil {
		return
	}
	if !e.Queue {
		labkit.Error(w, 400, "no_waiting_room", "this event has no waiting room")
		return
	}
	sess := sid(r)
	q := e.waiting
	q.mu.Lock()
	if _, ok := q.index[sess]; !ok {
		q.index[sess] = len(q.queue)
		q.queue = append(q.queue, sess)
	}
	q.mu.Unlock()
	pos, _ := s.position(e, sess)
	labkit.JSON(w, 200, map[string]any{"eventId": e.ID, "position": pos, "socket": "/ws/queue?event=" + strconv.Itoa(e.ID)})
}

func (s *server) queueSocket(w http.ResponseWriter, r *http.Request) {
	e := s.event(w, r.URL.Query().Get("event"))
	if e == nil {
		return
	}
	if !e.Queue {
		labkit.Error(w, 400, "no_waiting_room", "this event has no waiting room")
		return
	}
	sess := sid(r)
	if _, ok := s.position(e, sess); !ok {
		labkit.Error(w, 409, "not_queued", "join the waiting room first")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	ctx = conn.CloseRead(ctx)
	t := time.NewTicker(s.tick)
	defer t.Stop()
	last := -1
	for {
		pos, _ := s.position(e, sess)
		if pos == 0 {
			_ = wsjson.Write(ctx, conn, map[string]any{"type": "admitted", "eventId": e.ID})
			break
		}
		if pos != last {
			if wsjson.Write(ctx, conn, map[string]any{"type": "position", "position": pos}) != nil {
				return
			}
			last = pos
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
	// Keep the socket open until the client closes it.
	<-ctx.Done()
	_ = conn.Close(websocket.StatusNormalClosure, "")
}
