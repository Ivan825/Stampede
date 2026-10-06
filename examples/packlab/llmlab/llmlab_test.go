package llmlab

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

func stream(t *testing.T, url, prompt string, tokens int) string {
	t.Helper()
	body := `{"model":"lab-small","stream":true,"max_tokens":` + strconv.Itoa(tokens) + `,"messages":[{"role":"user","content":"` + prompt + `"}]}`
	resp, err := http.Post(url+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Error(err)
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// run starts n streams at once against a server built with fixes and
// returns it after they finish.
func run(t *testing.T, fixes string, n int, prompt string) *server {
	s, h := newServer(labkit.Config{Fixes: labkit.ParseFixes(fixes)})
	s.tokenDelay = 2 * time.Millisecond
	s.prefillPerChar = 50 * time.Microsecond
	srv := httptest.NewServer(h)
	defer srv.Close()
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out := stream(t, srv.URL, prompt, 20)
			if !strings.Contains(out, "data: [DONE]") || !strings.Contains(out, `"finish_reason":"stop"`) {
				t.Errorf("stream did not finish: %q", out)
			}
		}()
	}
	wg.Wait()
	return s
}

func TestStreamFormat(t *testing.T) {
	_, h := newServer(labkit.Config{Fast: true})
	srv := httptest.NewServer(h)
	defer srv.Close()
	out := stream(t, srv.URL, "hello", 3)
	if strings.Count(out, "data: ") != 6 { // role, 3 tokens, stop, [DONE]
		t.Fatalf("events: %q", out)
	}
	if !strings.Contains(out, `"usage":{"completion_tokens":3`) {
		t.Fatalf("usage: %q", out)
	}
}

func TestSlotsBottleneck(t *testing.T) {
	if p := run(t, "", 12, "hi").gen.Peak(); p > 4 {
		t.Fatalf("without the fix at most 4 generations run at once, saw %d", p)
	}
	if p := run(t, "batch", 12, "hi").gen.Peak(); p <= 4 {
		t.Fatalf("with batch more than 4 generations should overlap, saw %d", p)
	}
}

func TestPrefillBottleneck(t *testing.T) {
	long := strings.Repeat("x", 2000) // 100ms of prompt processing each
	if p := run(t, "batch", 4, long).pre.Peak(); p != 1 {
		t.Fatalf("without chunked prompt processing is serialised, saw %d at once", p)
	}
	if p := run(t, "batch,chunked", 4, long).pre.Peak(); p < 2 {
		t.Fatalf("with chunked prompts should be processed together, saw %d", p)
	}
}

func TestUsageBottleneck(t *testing.T) {
	// Re-tokenising the reply after each of 20 tokens costs 1+2+...+20.
	if w := run(t, "", 1, "hi").work.Load(); w != 210 {
		t.Fatalf("work %d, want 210", w)
	}
	if w := run(t, "usage", 1, "hi").work.Load(); w != 0 {
		t.Fatalf("work with the fix %d, want 0", w)
	}
}
