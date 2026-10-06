package gamelab

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

func start(t *testing.T, fixes string) (*server, string) {
	t.Helper()
	s, app, err := newServer(labkit.Config{Fixes: labkit.ParseFixes(fixes), Fast: true, Listen: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	return s, srv.URL
}

func login(t *testing.T, base, name string) string {
	t.Helper()
	resp, err := http.Post(base+"/api/login", "application/json", strings.NewReader(`{"player":"`+name+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return m["token"].(string)
}

// queue signs a player in, queues for a mode and returns the socket.
func queue(t *testing.T, base, name, mode string) *websocket.Conn {
	t.Helper()
	tok := login(t, base, name)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/api/matchmaking", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"queue","mode":"`+mode+`"}`)); err != nil {
		t.Fatal(err)
	}
	if m := read(t, c, "queued"); m["mode"] != mode {
		t.Fatalf("queued %v", m)
	}
	return c
}

func read(t *testing.T, c *websocket.Conn, typ string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %s: %v", typ, err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if m["type"] == typ {
			return m
		}
	}
}

func udp(t *testing.T, addr string) func(string) string {
	t.Helper()
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return func(msg string) string {
		if _, err := c.Write([]byte(msg)); err != nil {
			t.Fatal(err)
		}
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 512)
		n, err := c.Read(buf)
		if err != nil {
			t.Fatalf("%s: %v", msg, err)
		}
		return string(buf[:n])
	}
}

func TestMatchAndPlay(t *testing.T) {
	for _, fixes := range []string{"", "all"} {
		_, base := start(t, fixes)
		// An opponent within the starting window, so they meet before
		// bots are called in.
		bob := "bob"
		for i := 0; abs(rating(bob)-rating("alice")) > 100; i++ {
			bob = fmt.Sprint("bob-", i)
		}
		a := queue(t, base, "alice", "duel")
		b := queue(t, base, bob, "duel")
		ma, mb := read(t, a, "match"), read(t, b, "match")
		if ma["match"] != mb["match"] || ma["bots"] != 0.0 || ma["ticket"] == mb["ticket"] {
			t.Fatalf("fixes %q: alice got %v, bob got %v", fixes, ma, mb)
		}
		id := ma["match"].(string)
		send := udp(t, ma["server"].(string))
		if got := send("input " + id + " 1 up"); got != "error "+id+" not joined" {
			t.Fatalf("input before joining: %q", got)
		}
		if got := send("join " + id + " nope"); got != "error "+id+" bad ticket" {
			t.Fatalf("bad ticket: %q", got)
		}
		if got := send("join " + id + " " + ma["ticket"].(string)); !strings.HasPrefix(got, "joined "+id+" ") {
			t.Fatalf("join: %q", got)
		}
		if got := send("input " + id + " 7 left"); !strings.HasPrefix(got, "state "+id+" 7 ") {
			t.Fatalf("input: %q", got)
		}
		if got := send("ping x"); got != "pong x" {
			t.Fatalf("ping: %q", got)
		}
		if got := send("leave " + id); got != "bye "+id {
			t.Fatalf("leave: %q", got)
		}
	}
}

func TestBotsFillALonelyQueue(t *testing.T) {
	_, base := start(t, "")
	c := queue(t, base, "carol", "squad")
	m := read(t, c, "match")
	if m["bots"] != 3.0 || len(m["players"].([]any)) != 4 {
		t.Fatalf("match %v", m)
	}
}

func TestMatchmakingBottleneck(t *testing.T) {
	s, _ := start(t, "")
	now := time.Now()
	var q []*ticket
	for i := range 200 {
		// Ratings 300 apart: nobody is close enough to match yet.
		q = append(q, &ticket{p: &player{name: fmt.Sprint(i), rating: i * 300}, since: now})
	}
	before := s.compared.Load()
	if g := s.groupNearest(q, 2, now); len(g) != 0 {
		t.Fatalf("matched %d groups", len(g))
	}
	if got := s.compared.Load() - before; got != 200*200 {
		t.Errorf("without the fix each player is compared with every other: %d", got)
	}
	before = s.compared.Load()
	if g := s.groupSorted(q, 2, now); len(g) != 0 {
		t.Fatalf("matched %d groups", len(g))
	}
	if got := s.compared.Load() - before; got != 200 {
		t.Errorf("with the fix the queue is walked once: %d", got)
	}
	// Close ratings match either way.
	for i := range q {
		q[i].p.rating = 1000 + i
	}
	if a, b := len(s.groupNearest(q, 2, now)), len(s.groupSorted(q, 2, now)); a != 100 || b != 100 {
		t.Errorf("grouped %d and %d pairs, want 100", a, b)
	}
}

func TestLeaderboardBottleneck(t *testing.T) {
	ranks := map[string][]float64{}
	for _, fixes := range []string{"", "leaderboard"} {
		s, base := start(t, fixes)
		tok := login(t, base, "dave")
		before := s.sorted.Load()
		for _, score := range []int{10, 99990, 50000, 100001} {
			req, _ := http.NewRequest("POST", base+"/api/scores", strings.NewReader(fmt.Sprintf(`{"score": %d}`, score)))
			req.Header.Set("Authorization", "Bearer "+tok)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&m)
			resp.Body.Close()
			ranks[fixes] = append(ranks[fixes], m["rank"].(float64))
		}
		work := s.sorted.Load() - before
		if fixes == "" && work < 4*int64(Seeded(true)) {
			t.Errorf("without the fix every score re-sorts the board: %d", work)
		}
		if fixes != "" && work != 0 {
			t.Errorf("with the fix nothing is re-sorted or scanned: %d", work)
		}
		resp, err := http.Get(base + "/api/leaderboard/dave")
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		resp.Body.Close()
		if m["rank"] != 1.0 || m["score"] != 100001.0 {
			t.Errorf("fixes %q: dave %v", fixes, m)
		}
	}
	if fmt.Sprint(ranks[""]) != fmt.Sprint(ranks["leaderboard"]) || ranks[""][3] != 1 {
		t.Errorf("ranks differ: %v and %v", ranks[""], ranks["leaderboard"])
	}
}
