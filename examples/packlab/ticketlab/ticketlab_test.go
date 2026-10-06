package ticketlab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

func start(t *testing.T, fixes string) (*server, string) {
	s, h := newServer(labkit.Config{Fast: true, Fixes: labkit.ParseFixes(fixes)})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return s, srv.URL
}

// browser is a client with its own cookie jar, like one visitor.
func browser() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func call(t *testing.T, c *http.Client, method, u, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, u, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func TestNoSeatSoldTwice(t *testing.T) {
	for _, fixes := range []string{"", "all"} {
		_, base := start(t, fixes)
		var wg sync.WaitGroup
		var mu sync.Mutex
		won := 0
		for i := range 40 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c := browser()
				code, _ := call(t, c, "POST", base+"/api/holds", fmt.Sprintf(`{"eventId":2,"seats":["A%d","A%d"]}`, i%5+1, i%5+6))
				if code == 201 {
					if code, _ := call(t, c, "POST", base+"/api/orders", `{}`); code != 201 {
						t.Errorf("order: %d", code)
					}
					mu.Lock()
					won++
					mu.Unlock()
				} else if code != 409 {
					t.Errorf("hold: %d", code)
				}
			}()
		}
		wg.Wait()
		_, ev := call(t, browser(), "GET", base+"/api/events/2", "")
		if ev["oversold"] != false || int(ev["sold"].(float64)) != 2*won || won == 0 || won > 5 {
			t.Fatalf("fixes %q: %d winners, event %v", fixes, won, ev)
		}
	}
}

func TestWaitingRoomGatesHolds(t *testing.T) {
	_, base := start(t, "")
	c := browser()
	if code, _ := call(t, c, "POST", base+"/api/holds", `{"eventId":1,"quantity":2}`); code != 403 {
		t.Fatalf("hold before queueing: %d", code)
	}
	if code, m := call(t, c, "POST", base+"/api/queue/1/join", ""); code != 200 || m["position"] == nil {
		t.Fatalf("join: %d %v", code, m)
	}
	// Fast mode admits thousands a second; one visitor is admitted at once.
	if code, m := call(t, c, "POST", base+"/api/holds", `{"eventId":1,"quantity":2}`); code != 201 {
		t.Fatalf("hold after admission: %d %v", code, m)
	}
}

func TestLockBottleneck(t *testing.T) {
	for _, tc := range []struct {
		fixes string
		one   bool
	}{{"", true}, {"lock", false}} {
		s, base := start(t, tc.fixes)
		s.slow = 5 * time.Millisecond // widen the critical section so overlaps show
		var wg sync.WaitGroup
		for i := range 40 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				call(t, browser(), "GET", fmt.Sprintf("%s/api/events/%d/seats", base, i%18+3), "")
			}()
		}
		wg.Wait()
		if p := s.inflight.Peak(); (p == 1) != tc.one {
			t.Errorf("fixes %q: %d events locked at once", tc.fixes, p)
		}
	}
}

func TestSeatmapBottleneck(t *testing.T) {
	for _, tc := range []struct {
		fixes string
		most  int64
	}{{"", 1000}, {"seatmap", 200}} {
		s, base := start(t, tc.fixes)
		c := browser()
		for range 50 {
			call(t, c, "POST", base+"/api/holds", `{"eventId":5,"quantity":1}`)
			call(t, c, "POST", base+"/api/orders", `{}`)
		}
		before := s.replayed.Load()
		call(t, c, "GET", base+"/api/events/5/seats", "")
		got := s.replayed.Load() - before
		// Without the fix the map replays all 50 holds twice (sweep and
		// rebuild); with it only the last, not yet pruned, is looked at.
		if tc.fixes == "" && got != 100 {
			t.Errorf("replayed %d holds, want 100", got)
		}
		if tc.fixes != "" && got > 1 {
			t.Errorf("with the fix replayed %d holds, want at most the last one", got)
		}
	}
}

func TestQueueBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "queue"} {
		s, base := start(t, fixes)
		for range 100 {
			call(t, browser(), "POST", base+"/api/queue/1/join", "")
		}
		got := s.queueScans.Load()
		if fixes == "" && got < 100*99/2 {
			t.Errorf("without the fix each join scans the queue: %d", got)
		}
		if fixes != "" && got != 0 {
			t.Errorf("with the fix nothing is scanned: %d", got)
		}
	}
}
