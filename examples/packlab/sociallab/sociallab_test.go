package sociallab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

func start(t *testing.T, fixes string) (*server, string) {
	s, h := newServer(labkit.Config{Fast: true, Fixes: labkit.ParseFixes(fixes)})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return s, srv.URL
}

func call(t *testing.T, method, u, token, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, u, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func login(t *testing.T, base string, n int) string {
	t.Helper()
	code, m := call(t, "POST", base+"/api/login", "", fmt.Sprintf(`{"username":"user%04d","password":%q}`, n, Password))
	if code != 200 {
		t.Fatalf("login: %d %v", code, m)
	}
	return m["token"].(string)
}

func ids(m map[string]any) []float64 {
	var out []float64
	for _, p := range m["posts"].([]any) {
		out = append(out, p.(map[string]any)["id"].(float64))
	}
	return out
}

// The timeline fix must not change what anyone sees: the same two pages,
// and a new post from a followed account on top.
func TestFeedIsTheSameEitherWay(t *testing.T) {
	var pages [][]float64
	for _, fixes := range []string{"", "timeline"} {
		_, base := start(t, fixes)
		tok := login(t, base, 100)
		_, p1 := call(t, "GET", base+"/api/feed", tok, "")
		_, p2 := call(t, "GET", base+"/api/feed?before="+p1["next_cursor"].(string), tok, "")
		pages = append(pages, append(ids(p1), ids(p2)...))
		// Someone user0100 follows posts; it tops the feed.
		_, prof := call(t, "GET", base+"/api/users/user0100", tok, "")
		if prof["following"].(float64) < 50 {
			t.Fatalf("profile: %v", prof)
		}
		_, followee := call(t, "POST", base+"/api/users/user0042/follow", tok, "")
		if followee["following"] != true {
			t.Fatalf("follow: %v", followee)
		}
		_, _ = call(t, "GET", base+"/api/feed", tok, "") // rebuild after the follow
		code, np := call(t, "POST", base+"/api/posts", login(t, base, 42), `{"text":"hello followers"}`)
		if code != 201 {
			t.Fatalf("post: %d", code)
		}
		_, top := call(t, "GET", base+"/api/feed", tok, "")
		if ids(top)[0] != np["id"].(float64) {
			t.Fatalf("fixes %q: the new post is not on top: %v", fixes, ids(top)[:3])
		}
	}
	if len(pages[0]) != 2*pageSize || !reflect.DeepEqual(pages[0], pages[1]) {
		t.Fatalf("feeds differ:\n%v\n%v", pages[0], pages[1])
	}
}

func TestTimelineBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "timeline"} {
		s, base := start(t, fixes)
		tok := login(t, base, 200)
		u := s.byName["user0200"]
		all := 0
		for _, f := range u.following {
			all += len(s.users[f].posts)
		}
		call(t, "GET", base+"/api/feed", tok, "") // the fix builds the timeline here
		before := s.examined.Load()
		call(t, "GET", base+"/api/feed", tok, "")
		got := s.examined.Load() - before
		if fixes == "" && got != int64(all) {
			t.Errorf("without the fix a feed looked at %d posts, want all %d", got, all)
		}
		if fixes != "" && got > pageSize*(Celebrities+1) {
			t.Errorf("with the fix a feed looked at %d posts (all: %d)", got, all)
		}
	}
}

func TestLikes(t *testing.T) {
	for _, fixes := range []string{"", "all"} {
		s, base := start(t, fixes)
		var wg sync.WaitGroup
		for i := range 50 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tok := login(t, base, 1000+i)
				call(t, "POST", base+"/api/posts/1/like", tok, "")
				call(t, "POST", base+"/api/posts/1/like", tok, "") // a repeat changes nothing
			}()
		}
		wg.Wait()
		tok := login(t, base, 1000)
		if _, m := call(t, "DELETE", base+"/api/posts/1/like", tok, ""); m["likes"] != float64(49) {
			t.Fatalf("fixes %q: after 50 likes and an unlike: %v", fixes, m)
		}
		if fixes == "" {
			if n := s.likersScan.Load(); n < 50*49 {
				t.Errorf("without the fix likes scanned %d likers", n)
			}
		} else if n := s.likersScan.Load(); n != 0 {
			t.Errorf("with the fix likes scanned %d likers", n)
		}
	}
}

func TestLikesLockBottleneck(t *testing.T) {
	for _, tc := range []struct {
		fixes string
		one   bool
	}{{"", true}, {"likes", false}} {
		s, base := start(t, tc.fixes)
		s.slow = 5 * time.Millisecond
		var wg sync.WaitGroup
		for i := range 30 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				call(t, "POST", fmt.Sprintf("%s/api/posts/%d/like", base, 100+i), login(t, base, 2000+i), "")
			}()
		}
		wg.Wait()
		if p := s.likeGauge.Peak(); (p == 1) != tc.one {
			t.Errorf("fixes %q: %d likes in progress at once", tc.fixes, p)
		}
	}
}

func TestNotifications(t *testing.T) {
	for _, fixes := range []string{"", "notify"} {
		s, base := start(t, fixes)
		author := login(t, base, 300)
		fan := login(t, base, 301)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/api/notifications/ws",
			&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + author}}})
		if err != nil {
			t.Fatal(err)
		}
		read := func() map[string]any {
			_, b, err := c.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			return m
		}
		if m := read(); m["type"] != "welcome" {
			t.Fatalf("first message: %v", m)
		}
		_, posts := call(t, "GET", base+"/api/users/user0300/posts?limit=1", fan, "")
		id := posts["posts"].([]any)[0].(map[string]any)["id"].(float64)
		call(t, "POST", fmt.Sprintf("%s/api/posts/%.0f/comments", base, id), fan, `{"text":"nice"}`)
		if m := read(); m["kind"] != "comment" || m["from"] != "user0301" {
			t.Fatalf("notification: %v", m)
		}
		_, list := call(t, "GET", base+"/api/notifications", author, "")
		if len(list["notifications"].([]any)) != 1 {
			t.Fatalf("notifications: %v", list)
		}
		inline := s.inlineWrites.Load()
		if (fixes == "") != (inline > 0) {
			t.Errorf("fixes %q: %d socket writes inside requests", fixes, inline)
		}
		c.CloseNow()
	}
}

func TestEveryoneHasPosted(t *testing.T) {
	s, _ := start(t, "")
	for _, u := range s.users {
		if len(u.posts) == 0 {
			t.Fatalf("%s has no posts", u.name)
		}
	}
}
