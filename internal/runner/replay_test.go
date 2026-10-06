package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/scenario"
)

func TestReplaySendsTheRecordingOnTime(t *testing.T) {
	type hit struct {
		at          time.Time
		method, uri string
		body, ctype string
	}
	var mu sync.Mutex
	var hits []hit
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits = append(hits, hit{time.Now(), r.Method, r.RequestURI, string(b), r.Header.Get("Content-Type")})
		mu.Unlock()
	}))
	defer target.Close()

	// 40 requests over 4 recorded seconds, replayed at speed 2.
	dir := t.TempDir()
	var log strings.Builder
	base := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	// HAR timestamps have sub-second resolution, unlike access logs.
	var want []string
	for i := range 40 {
		want = append(want, fmt.Sprintf("/api/items/%d?i=%d", i%5, i))
	}
	var entries []string
	for i, u := range want {
		at := base.Add(time.Duration(i) * 100 * time.Millisecond).Format(time.RFC3339Nano)
		e := fmt.Sprintf(`{"startedDateTime": %q, "request": {"method": "GET", "url": "https://shop.example%s"}}`, at, u)
		if i%10 == 9 {
			e = fmt.Sprintf(`{"startedDateTime": %q, "request": {"method": "POST", "url": "https://shop.example/api/orders", "postData": {"mimeType": "application/json", "text": "{\"n\":%d}"}}}`, at, i)
			want[i] = "POST /api/orders {\"n\":" + fmt.Sprint(i) + "}"
		} else {
			want[i] = "GET " + u + " "
		}
		entries = append(entries, e)
	}
	log.WriteString(`{"log": {"entries": [` + strings.Join(entries, ",") + `]}}`)
	if err := os.WriteFile(filepath.Join(dir, "session.har"), []byte(log.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`
metadata: {name: replay}
target: {baseURL: %q}
load: {mode: replay, replay: {file: session.har, speed: 2}}`, target.URL)
	if err := os.WriteFile(filepath.Join(dir, "s.yaml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := scenario.LoadFile(filepath.Join(dir, "s.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), Options{Scenario: s, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 40 || rep.Overall.Requests != 40 || rep.Overall.Dropped != 0 {
		t.Fatalf("target saw %d, report %d requests, %d dropped", len(hits), rep.Overall.Requests, rep.Overall.Dropped)
	}
	got := map[string]int{}
	for _, h := range hits {
		got[h.method+" "+h.uri+" "+h.body]++
		if h.method == "POST" && h.ctype != "application/json" {
			t.Errorf("POST content type %q", h.ctype)
		}
	}
	for _, w := range want {
		if got[w] != 1 {
			t.Errorf("request %q sent %d times", w, got[w])
		}
	}
	// 100ms apart at speed 2 is 50ms apart: the 40 requests span ~1.95s.
	span := hits[len(hits)-1].at.Sub(hits[0].at)
	if span < 1800*time.Millisecond || span > 2300*time.Millisecond {
		t.Errorf("requests spanned %v, want about 1.95s", span)
	}
	var names []string
	for _, j := range rep.Journeys {
		names = append(names, j.Name)
	}
	if strings.Join(names, ",") != "GET api.items.{id},POST api.orders" {
		t.Errorf("journeys = %v", names)
	}
	if rep.Load.Executor != scenario.ExecReplay {
		t.Errorf("executor = %q", rep.Load.Executor)
	}
}
