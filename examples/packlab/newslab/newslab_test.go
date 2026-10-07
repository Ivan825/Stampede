package newslab

import (
	"io"
	"net/http"
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

func get(t *testing.T, u string, hdr ...string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", u, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func publish(t *testing.T, base, body string) {
	t.Helper()
	req, _ := http.NewRequest("POST", base+"/api/articles", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+EditorToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 201 {
		t.Fatalf("publish: %v %v", err, resp)
	}
	resp.Body.Close()
}

func TestCacheHeaders(t *testing.T) {
	_, base := start(t, "")
	r1, body := get(t, base+"/")
	if r1.Header.Get("X-Cache") != "MISS" || !strings.Contains(body, `class="top-story"`) || !strings.Contains(body, "schema.org/NewsArticle") {
		t.Fatalf("first home page: %v", r1.Header)
	}
	r2, _ := get(t, base+"/")
	etag := r2.Header.Get("ETag")
	if r2.Header.Get("X-Cache") != "HIT" || etag == "" || r2.Header.Get("Age") == "" || !strings.Contains(r2.Header.Get("Cache-Control"), "s-maxage=60") {
		t.Fatalf("second home page: %v", r2.Header)
	}
	if r3, _ := get(t, base+"/", "If-None-Match", etag); r3.StatusCode != 304 {
		t.Fatalf("conditional request: %d", r3.StatusCode)
	}
	if r4, _ := get(t, base+"/articles/story-99999"); r4.StatusCode != 404 {
		t.Fatalf("missing article: %d", r4.StatusCode)
	}
}

func TestStampedeBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "stampede"} {
		s, base := start(t, fixes)
		s.cost["article"] = 100 * time.Millisecond // all requests arrive while the first renders
		var wg sync.WaitGroup
		ready := make(chan struct{})
		for range 40 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-ready
				if r, _ := get(t, base+"/articles/story-00042"); r.StatusCode != 200 {
					t.Errorf("status %d", r.StatusCode)
				}
			}()
		}
		close(ready)
		wg.Wait()
		n := s.renders.Load()
		if fixes == "" && n != 40 {
			t.Errorf("without the fix 40 requests for a cold page made %d renders, want 40", n)
		}
		if fixes != "" && n != 1 {
			t.Errorf("with the fix 40 requests made %d renders, want 1", n)
		}
	}
}

func TestPurgeBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "purge"} {
		s, base := start(t, fixes)
		get(t, base+"/")
		get(t, base+"/section/sport")
		for _, slug := range []string{"story-00001", "story-00002", "story-00003"} {
			get(t, base+"/articles/"+slug)
		}
		publish(t, base, `{"title":"Storm hits the coast","section":"world","breaking":true}`)
		renders := s.renders.Load()
		r, body := get(t, base+"/")
		if r.Header.Get("X-Cache") != "MISS" || !strings.Contains(body, "Breaking") || !strings.Contains(body, "Storm hits the coast") {
			t.Fatalf("fixes %q: home after publishing: %v", fixes, r.Header)
		}
		r, _ = get(t, base+"/articles/story-00002")
		kept := r.Header.Get("X-Cache") == "HIT"
		if fixes == "" && kept {
			t.Errorf("without the fix an article page survived the purge")
		}
		if fixes != "" && !kept {
			t.Errorf("with the fix an unrelated article page was purged")
		}
		if fixes != "" && s.renders.Load()-renders != 1 {
			t.Errorf("with the fix only the home page should render again")
		}
	}
}

func TestCacheKeyBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "cachekey"} {
		s, base := start(t, fixes)
		for _, q := range []string{"?utm_source=social&fbclid=a1", "?utm_source=social&fbclid=b2", "?fbclid=c3&utm_medium=share", ""} {
			get(t, base+"/articles/story-00007"+q)
		}
		// Real parameters still make different pages.
		get(t, base+"/section/sport?page=1")
		get(t, base+"/section/sport?page=2")
		n := s.renders.Load()
		if fixes == "" && n != 6 {
			t.Errorf("without the fix: %d renders, want 6 (each tracking link misses)", n)
		}
		if fixes != "" && n != 3 {
			t.Errorf("with the fix: %d renders, want 3 (one article, two section pages)", n)
		}
	}
}
