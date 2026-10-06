package apilab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

func do(t *testing.T, method, u, key, body string, hdr ...string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, u, strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp, m
}

func start(t *testing.T, fixes string) (*server, string) {
	return startCfg(t, fixes, true)
}

func startCfg(t *testing.T, fixes string, fast bool) (*server, string) {
	s, h := newServer(labkit.Config{Fast: fast, Fixes: labkit.ParseFixes(fixes)})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return s, srv.URL
}

func TestRateLimit(t *testing.T) {
	for _, fixes := range []string{"", "limiter"} {
		_, base := start(t, fixes)
		ok, limited := 0, 0
		for range 30 {
			resp, _ := do(t, "GET", base+"/v1/items?limit=1", "sk_test_free", "")
			switch resp.StatusCode {
			case 200:
				ok++
			case 429:
				limited++
				if resp.Header.Get("Retry-After") == "" {
					t.Fatalf("fixes %q: 429 without Retry-After", fixes)
				}
			default:
				t.Fatalf("status %d", resp.StatusCode)
			}
		}
		// The free plan allows 5 a second (a burst of 10, plus refill, with the fix).
		if ok < 5 || ok > 12 || limited == 0 {
			t.Errorf("fixes %q: %d allowed, %d limited", fixes, ok, limited)
		}
	}
}

func TestIdempotentCreate(t *testing.T) {
	_, base := start(t, "")
	r1, a := do(t, "POST", base+"/v1/items", "sk_test_00001", `{"name":"x","price":1}`, "Idempotency-Key", "k1")
	r2, b := do(t, "POST", base+"/v1/items", "sk_test_00001", `{"name":"y","price":2}`, "Idempotency-Key", "k1")
	if r1.StatusCode != 201 || r2.StatusCode != 201 || a["id"] != b["id"] || r2.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("%v %v / %v %v", r1.StatusCode, a, r2.StatusCode, b)
	}
}

func TestKeysBottleneck(t *testing.T) {
	s, base := start(t, "")
	do(t, "GET", base+"/v1/usage", "sk_test_00001", "")
	if got := s.scanned.Load(); got != Keys+1 {
		t.Fatalf("without the fix one request scans every key: %d, want %d", got, Keys+1)
	}
	s, base = start(t, "keys")
	do(t, "GET", base+"/v1/usage", "sk_test_00001", "")
	if got := s.scanned.Load(); got != 0 {
		t.Fatalf("with the fix nothing is scanned, got %d", got)
	}
}

func TestLimiterBottleneck(t *testing.T) {
	// Refused attempts stay in the window log, so a flood makes every
	// later check longer.
	s, base := start(t, "")
	for range 100 {
		do(t, "GET", base+"/v1/usage", "sk_test_free", "")
	}
	if got := s.swept.Load(); got < 1000 {
		t.Fatalf("flooding should grow the log the limiter filters: %d entries swept", got)
	}
	s, base = start(t, "limiter")
	for range 100 {
		do(t, "GET", base+"/v1/usage", "sk_test_free", "")
	}
	if got := s.swept.Load(); got != 0 {
		t.Fatalf("with token buckets nothing is swept, got %d", got)
	}
}

func TestWebhooksBottleneck(t *testing.T) {
	for _, tc := range []struct {
		fixes string
		one   bool
	}{{"", true}, {"webhooks", false}} {
		s, base := startCfg(t, tc.fixes, false) // 20ms per delivery
		var wg sync.WaitGroup
		ids := make([]string, 8)
		for i := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, m := do(t, "POST", base+"/v1/webhooks/test", "sk_test_00002", "")
				ids[i], _ = m["id"].(string)
			}()
		}
		wg.Wait()
		deadline := time.Now().Add(5 * time.Second)
		for _, id := range ids {
			for {
				_, m := do(t, "GET", base+"/v1/deliveries/"+id, "sk_test_00003", "")
				if m["status"] == "delivered" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("delivery %s not delivered", id)
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		if p := s.delivering.Peak(); (p == 1) != tc.one {
			t.Errorf("fixes %q: %d deliveries at once", tc.fixes, p)
		}
	}
}
