package edgelab

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func get(t *testing.T, u string) (*http.Response, string) {
	t.Helper()
	resp, err := noRedirect.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestColdThenWarm(t *testing.T) {
	s, base := start(t, "")
	r1, _ := get(t, base+"/api/hello?name=a")
	r2, body := get(t, base+"/api/hello?name=b")
	if r1.Header.Get("X-Cold-Start") != "true" || r2.Header.Get("X-Cold-Start") != "false" || !strings.Contains(body, "hello b") {
		t.Fatalf("cold %q then %q", r1.Header.Get("X-Cold-Start"), r2.Header.Get("X-Cold-Start"))
	}
	if r1.Header.Get("X-Function-Instance") != r2.Header.Get("X-Function-Instance") || !strings.Contains(r1.Header.Get("Server-Timing"), "init;dur=") ||
		r1.Header.Get("Function-Execution-Id") == "" {
		t.Fatalf("headers: %v / %v", r1.Header, r2.Header)
	}
	// An instance idle past the platform's limit is reclaimed.
	s.idleTTL = 20 * time.Millisecond
	time.Sleep(40 * time.Millisecond)
	if r3, _ := get(t, base+"/api/hello"); r3.Header.Get("X-Cold-Start") != "true" {
		t.Fatal("an idle instance was not reclaimed")
	}
	r4, _ := get(t, base+"/r/go1234")
	if r4.StatusCode != 301 || r4.Header.Get("Location") != "https://example.com/articles/1234" {
		t.Fatalf("redirect: %d %v", r4.StatusCode, r4.Header)
	}
	resp, err := http.Post(base+"/api/links", "application/json", strings.NewReader(`{"url":"https://example.com/x"}`))
	if err != nil || resp.StatusCode != 201 {
		t.Fatalf("create: %v %v", err, resp)
	}
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	if r, _ := get(t, base+"/r/"+m["code"].(string)); r.StatusCode != 301 {
		t.Fatalf("new link: %d", r.StatusCode)
	}
}

func TestInitBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "init"} {
		s, base := start(t, fixes)
		get(t, base+"/api/hello")
		want := int64(modules)
		if fixes != "" {
			want = modules / 10
		}
		if n := s.loaded.Load(); n != want {
			t.Errorf("fixes %q: a cold start loaded %d modules, want %d", fixes, n, want)
		}
	}
}

func TestPrewarmBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "prewarm"} {
		s, base := start(t, fixes)
		var wg sync.WaitGroup
		var cold atomic.Int64
		for range provisioned {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if r, _ := get(t, base+"/api/links/go1500/stats"); r.Header.Get("X-Cold-Start") == "true" {
					cold.Add(1)
				}
			}()
		}
		wg.Wait()
		if fixes == "" && cold.Load() == 0 {
			t.Error("without the fix a first burst started nothing cold")
		}
		if fixes != "" && (cold.Load() != 0 || s.colds.Load() != 0) {
			t.Errorf("with the fix %d of %d requests started cold", cold.Load(), provisioned)
		}
	}
}

func TestPoolBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "pool"} {
		s, base := start(t, fixes)
		for range 10 {
			get(t, base+"/r/go1001")
		}
		want := int64(10)
		if fixes != "" {
			want = 1 // opened once, at the instance's cold start
		}
		if n := s.connects.Load(); n != want {
			t.Errorf("fixes %q: ten invocations opened %d connections, want %d", fixes, n, want)
		}
	}
}

func TestThrottle(t *testing.T) {
	for _, fixes := range []string{"", "all"} {
		_, base := start(t, fixes)
		var wg sync.WaitGroup
		var ok, throttled, other atomic.Int64
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, _ := get(t, base+"/api/preview?url=https://example.com")
				switch {
				case r.StatusCode == 200 && r.Header.Get("Content-Type") == "image/x-portable-graymap":
					ok.Add(1)
				case r.StatusCode == 429 && r.Header.Get("Retry-After") == "1":
					throttled.Add(1)
				default:
					other.Add(1)
				}
			}()
		}
		wg.Wait()
		if ok.Load() < 1 || ok.Load() > 5 || throttled.Load() == 0 || other.Load() != 0 {
			t.Fatalf("fixes %q: %d ok, %d throttled, %d other", fixes, ok.Load(), throttled.Load(), other.Load())
		}
	}
}
