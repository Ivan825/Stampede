package examlab

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
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
	code, m := call(t, "POST", base+"/api/login", "", fmt.Sprintf(`{"studentId":"s%05d","password":%q}`, n, Password))
	if code != 200 {
		t.Fatalf("login: %d %v", code, m)
	}
	return m["token"].(string)
}

func begin(t *testing.T, base, tok string) string {
	t.Helper()
	code, m := call(t, "POST", base+"/api/exams/EX-101/attempts", tok, "")
	if code != 201 {
		t.Fatalf("start: %d %v", code, m)
	}
	return m["attemptId"].(string)
}

func TestExamFlow(t *testing.T) {
	for _, fixes := range []string{"", "all"} {
		_, base := start(t, fixes)
		tok := login(t, base, 1)
		at := begin(t, base, tok)
		if code, q := call(t, "GET", base+"/api/attempts/"+at+"/questions/1", tok, ""); code != 200 || len(q["choices"].([]any)) != 4 {
			t.Fatalf("question: %d %v", code, q)
		}
		for n := 1; n <= 5; n++ {
			if code, m := call(t, "PUT", fmt.Sprintf("%s/api/attempts/%s/answers/%d", base, at, n), tok, `{"choice":2}`); code != 200 || m["answered"] != float64(n) {
				t.Fatalf("save %d: %d %v", n, code, m)
			}
		}
		// Changing an answer does not add one.
		call(t, "PUT", base+"/api/attempts/"+at+"/answers/3", tok, `{"choice":1}`)
		if _, m := call(t, "GET", base+"/api/attempts/"+at, tok, ""); m["answered"] != float64(5) {
			t.Fatalf("status: %v", m)
		}
		code, m := call(t, "POST", base+"/api/attempts/"+at+"/submit", tok, "")
		if code != 200 || m["answered"] != float64(5) || m["score"].(float64) > 5 {
			t.Fatalf("submit: %d %v", code, m)
		}
		if code, _ := call(t, "POST", base+"/api/attempts/"+at+"/submit", tok, ""); code != 409 {
			t.Fatalf("second submit: %d", code)
		}
		if code, _ := call(t, "PUT", base+"/api/attempts/"+at+"/answers/6", tok, `{"choice":0}`); code != 409 {
			t.Fatalf("save after submitting: %d", code)
		}
		if code, _ := call(t, "GET", base+"/api/attempts/"+at, login(t, base, 2), ""); code != 404 {
			t.Fatalf("someone else's attempt: %d", code)
		}
	}
}

func TestStartBottleneck(t *testing.T) {
	for _, tc := range []struct {
		fixes string
		one   bool
	}{{"", true}, {"start", false}} {
		s, base := start(t, tc.fixes)
		s.dbWrite = 3 * time.Millisecond
		var wg sync.WaitGroup
		for i := range 30 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				begin(t, base, login(t, base, 100+i))
			}()
		}
		wg.Wait()
		if p := s.startGauge.Peak(); (p == 1) != tc.one {
			t.Errorf("fixes %q: %d attempts started at once", tc.fixes, p)
		}
	}
}

func TestAutosaveBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "autosave"} {
		s, base := start(t, fixes)
		a, b := login(t, base, 10), login(t, base, 11)
		atA, atB := begin(t, base, a), begin(t, base, b)
		for n := 1; n <= 10; n++ {
			call(t, "PUT", fmt.Sprintf("%s/api/attempts/%s/answers/%d", base, atA, n), a, `{"choice":0}`)
			call(t, "PUT", fmt.Sprintf("%s/api/attempts/%s/answers/%d", base, atB, n), b, `{"choice":1}`)
		}
		got := s.scanned.Load()
		// Each of the 20 saves scans the whole log so far: 1 + 2 + ... + 20.
		if fixes == "" && got != 210 {
			t.Errorf("without the fix saves scanned %d log entries, want 210", got)
		}
		if fixes != "" && got != 0 {
			t.Errorf("with the fix saves scanned %d log entries", got)
		}
	}
}

func essay(rng *rand.Rand) string {
	var b strings.Builder
	b.WriteString("Essay on the causes of the industrial revolution. ")
	for range 1500 {
		b.WriteByte(byte('a' + rng.IntN(26)))
	}
	return b.String()
}

func TestSimilarity(t *testing.T) {
	for _, fixes := range []string{"", "similarity"} {
		s, base := start(t, fixes)
		rng := rand.New(rand.NewPCG(1, 2))
		var texts []string
		for i := range 30 {
			texts = append(texts, essay(rng))
			body, _ := json.Marshal(map[string]string{"text": texts[i]})
			if code, m := call(t, "POST", base+"/api/assignments/A-101/submissions", login(t, base, 200+i), string(body)); code != 201 || m["late"] != false {
				t.Fatalf("submit: %d %v", code, m)
			}
		}
		body, _ := json.Marshal(map[string]string{"text": texts[4]})
		_, copied := call(t, "POST", base+"/api/assignments/A-101/submissions", login(t, base, 300), string(body))
		body, _ = json.Marshal(map[string]string{"text": essay(rng)})
		_, fresh := call(t, "POST", base+"/api/assignments/A-101/submissions", login(t, base, 301), string(body))
		if copied["similarity"].(float64) < 0.9 || fresh["similarity"].(float64) > 0.2 {
			t.Fatalf("fixes %q: copied %v, fresh %v", fixes, copied["similarity"], fresh["similarity"])
		}
		n := s.compared.Load()
		if fixes == "" && n != 31*32/2 {
			t.Errorf("without the fix %d comparisons, want %d", n, 31*32/2)
		}
		if fixes != "" && n != 0 {
			t.Errorf("with the fix %d pairwise comparisons", n)
		}
	}
}

func TestLiveChannel(t *testing.T) {
	_, base := start(t, "")
	tok := login(t, base, 50)
	at := begin(t, base, tok)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/api/exams/EX-101/live?attempt="+at,
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	read := func(want string) map[string]any {
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				t.Fatalf("waiting for %s: %v", want, err)
			}
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			if m["type"] == want {
				return m
			}
		}
	}
	if m := read("timer"); m["remaining"].(float64) < 3500 {
		t.Fatalf("timer: %v", m)
	}
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"heartbeat","focus":true}`))
	read("ack")
	req, _ := http.NewRequest("POST", base+"/api/exams/EX-101/announcements", strings.NewReader(`{"text":"Question 7: read B as C"}`))
	req.Header.Set("Authorization", "Bearer "+StaffToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("announce: %v %v", err, resp)
	}
	resp.Body.Close()
	if m := read("announcement"); !strings.Contains(m["text"].(string), "Question 7") {
		t.Fatalf("announcement: %v", m)
	}
}
