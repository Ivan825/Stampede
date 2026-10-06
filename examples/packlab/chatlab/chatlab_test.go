package chatlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

type client struct {
	t    *testing.T
	conn *websocket.Conn
}

func connect(t *testing.T, base, user, room string) *client {
	t.Helper()
	resp, err := http.Post(base+"/api/login", "application/json", strings.NewReader(`{"user":"`+user+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/ws?room="+room, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + m["token"]}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	cl := &client{t: t, conn: c}
	cl.expect("welcome")
	return cl
}

func (c *client) send(v string) {
	if err := c.conn.Write(context.Background(), websocket.MessageText, []byte(v)); err != nil {
		c.t.Fatal(err)
	}
}

// expect reads until a message of the given type and returns it.
func (c *client) expect(typ string) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, b, err := c.conn.Read(ctx)
		if err != nil {
			c.t.Fatalf("waiting for %s: %v", typ, err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if m["type"] == typ {
			return m
		}
	}
}

func start(t *testing.T, fixes string) (*server, string) {
	s, h := newServer(labkit.Config{Fixes: labkit.ParseFixes(fixes)})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return s, srv.URL
}

func TestFanOutAndAck(t *testing.T) {
	for _, fixes := range []string{"", "all"} {
		_, base := start(t, fixes)
		a := connect(t, base, "alice", "general")
		b := connect(t, base, "bob", "general")
		a.send(`{"type":"say","text":"hi bob"}`)
		if m := a.expect("ack"); m["id"] == nil {
			t.Fatalf("ack %v", m)
		}
		if m := b.expect("message"); m["text"] != "hi bob" || m["user"] != "alice" {
			t.Fatalf("fixes %q: bob got %v", fixes, m)
		}
		b.send(`{"type":"ping"}`)
		b.expect("pong")
	}
}

func TestHistoryBottleneck(t *testing.T) {
	for _, tc := range []struct {
		fixes   string
		encoded int64
	}{{"", 300}, {"history", 10}} {
		s, base := start(t, tc.fixes)
		a := connect(t, base, "alice", "random")
		for range 300 {
			a.send(`{"type":"say","text":"x"}`)
			a.expect("ack")
		}
		before := s.encoded.Load()
		a.send(`{"type":"history","limit":10}`)
		if m := a.expect("history"); len(m["messages"].([]any)) != 10 {
			t.Fatalf("history %v", m)
		}
		if got := s.encoded.Load() - before; got != tc.encoded {
			t.Errorf("fixes %q: encoded %d messages for a history of 10, want %d", tc.fixes, got, tc.encoded)
		}
	}
}

func TestPresenceBottleneck(t *testing.T) {
	_, base := start(t, "")
	a := connect(t, base, "alice", "sales")
	connect(t, base, "bob", "sales")
	if m := a.expect("presence"); m["members"] == nil {
		t.Fatalf("without the fix presence carries the member list: %v", m)
	}
	_, base = start(t, "presence")
	a = connect(t, base, "alice", "sales")
	connect(t, base, "bob", "sales")
	if m := a.expect("presence"); m["members"] != nil || m["count"] == nil {
		t.Fatalf("with the fix presence carries only a count: %v", m)
	}
}
