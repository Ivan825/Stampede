package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/store/db"
)

type sseMsg struct {
	event string
	data  []byte
	// t orders point events so a reconnecting client never sees duplicates.
	t float64
}

// hub fans live messages for one run out to its subscribers. A slow
// subscriber loses messages rather than slowing the run; clients recover by
// reloading the timeline.
type hub struct {
	mu     sync.Mutex
	subs   map[chan sseMsg]struct{}
	closed bool
}

func newHub() *hub { return &hub{subs: map[chan sseMsg]struct{}{}} }

func (h *hub) subscribe() (chan sseMsg, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, false
	}
	ch := make(chan sseMsg, 256)
	h.subs[ch] = struct{}{}
	return ch, true
}

func (h *hub) unsubscribe(ch chan sseMsg) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
}

func (h *hub) publish(event string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	m := sseMsg{event: event, data: b}
	if p, ok := v.(gen.Point); ok {
		m.t = p.T
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- m:
		default:
		}
	}
}

func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for ch := range h.subs {
		close(ch)
	}
	h.subs = map[chan sseMsg]struct{}{}
}

// streamRun serves GET /runs/{runId}/live as server-sent events: the
// current status, every point recorded so far, then live updates until the
// run ends.
func (s *Server) streamRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := need(ctx, auth.PermView)
	if err != nil {
		s.responseError(w, r, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "runId"))
	if err != nil {
		writeError(w, &apiError{status: 400, code: "bad_request", msg: "invalid run id"})
		return
	}
	row, err := s.st.GetRun(ctx, db.GetRunParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		s.responseError(w, r, notFoundOr(err, "run"))
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}

	// Subscribe before reading the backlog so nothing falls in between.
	var live chan sseMsg
	if a := s.runs.get(id); a != nil {
		if ch, ok := a.hub.subscribe(); ok {
			live = ch
			defer a.hub.unsubscribe(ch)
		}
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(event string, data []byte) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	}
	b, _ := json.Marshal(runOf(row))
	send("status", b)

	last := -1.0
	pts, err := s.st.ListRunPoints(ctx, id)
	if err == nil {
		for _, pt := range pts {
			gp := pointRow(pt)
			b, _ := json.Marshal(gp)
			send("point", b)
			last = gp.T
		}
	}
	fl.Flush()

	if live == nil {
		// Finished, or not started on this replica: send the final status.
		if row, err := s.st.GetRun(ctx, db.GetRunParams{ID: id, OrgID: p.OrgID}); err == nil {
			b, _ := json.Marshal(runOf(row))
			send("status", b)
		}
		send("end", []byte("{}"))
		fl.Flush()
		return
	}

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case m, ok := <-live:
			if !ok {
				if row, err := s.st.GetRun(ctx, db.GetRunParams{ID: id, OrgID: p.OrgID}); err == nil {
					b, _ := json.Marshal(runOf(row))
					send("status", b)
				}
				send("end", []byte("{}"))
				fl.Flush()
				return
			}
			if m.event == "point" && m.t <= last {
				continue
			}
			send(m.event, m.data)
			fl.Flush()
		}
	}
}

func pointRow(r db.ListRunPointsRow) gen.Point {
	it := int(r.Iterations)
	lag := r.SchedLag
	return gen.Point{
		T: float64(r.Interval), Rps: r.Rps, ErrorRate: r.ErrorRate, P50: r.P50, P95: r.P95, P99: r.P99,
		Vus: int(r.Vus), Planned: r.Planned, Dropped: int(r.Dropped), Iterations: &it, SchedLagP99: &lag,
	}
}
