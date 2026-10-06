package coordinator_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/worker"
)

// TestReplaySplitsTheRecordingAcrossWorkers checks that two workers
// replaying one recording send every recorded request exactly once.
func TestReplaySplitsTheRecordingAcrossWorkers(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.RequestURI]++
		mu.Unlock()
	}))
	defer srv.Close()

	var lines strings.Builder
	base := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	for i := range 60 {
		at := base.Add(time.Duration(i/20) * time.Second).Format("02/Jan/2006:15:04:05 -0700")
		fmt.Fprintf(&lines, "10.0.0.1 - - [%s] \"GET /p/%d?n=%d HTTP/1.1\" 200 1\n", at, i%3, i)
	}
	file := filepath.Join(t.TempDir(), "access.log")
	if err := os.WriteFile(file, []byte(lines.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	c := newCluster(t, coordinator.Config{})
	c.addWorker(worker.Config{Name: "a"})
	c.addWorker(worker.Config{Name: "b"})
	c.waitConnected(2, 5*time.Second)
	r, err := c.coord.Start(context.Background(), coordinator.RunSpec{
		ID: "replay", StartDelay: 500 * time.Millisecond, Scenario: []byte(fmt.Sprintf(`
metadata: {name: replay}
target: {baseURL: %q}
load: {mode: replay, replay: {file: %q}}`, srv.URL, file)),
	})
	if err != nil {
		t.Fatal(err)
	}
	out := collect(t, r, 20*time.Second)
	if out.err != nil {
		t.Fatal(out.err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 60 {
		t.Fatalf("target saw %d distinct requests, want 60", len(seen))
	}
	for uri, n := range seen {
		if n != 1 {
			t.Errorf("%s sent %d times", uri, n)
		}
	}
}
