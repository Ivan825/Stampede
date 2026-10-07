package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Ivan825/Stampede/internal/pack"
)

func TestPackCreate(t *testing.T) {
	dir := t.TempDir()
	out, err := runCLI(t, "pack", "create", "fintech", "--dir", dir)
	if err != nil {
		t.Fatalf("create: %v %s", err, out)
	}
	root := filepath.Join(dir, "fintech")
	for _, f := range []string{"pack.yaml", "README.md", "journeys/everyday.yaml", "stresses/spike.yaml", "targets.yaml", "data/searches.csv"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
		if !strings.Contains(out, filepath.FromSlash(f)) {
			t.Errorf("output does not list %s: %s", f, out)
		}
	}
	p, err := pack.Load(root)
	if err != nil || p.Name != "fintech" || p.Title != "Fintech" || p.Variables["TARGET_URL"].Description == "" {
		t.Fatalf("load: %+v %v", p, err)
	}

	// The scaffold works as created: every journey passes its dry run.
	var searched atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("q"); q == "shoes" || q == "lamp" || q == "book" {
			searched.Store(true)
		}
	}))
	t.Cleanup(srv.Close)
	out, err = runCLI(t, "pack", "test", root, "--target", srv.URL)
	if err != nil || !strings.Contains(out, "Every journey in fintech works") {
		t.Errorf("pack test: %v %s", err, out)
	}
	if !searched.Load() {
		t.Error("the search journey did not read data/searches.csv")
	}

	if _, err := runCLI(t, "pack", "create", "fintech", "--dir", dir); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second create: %v", err)
	}
	if _, err := runCLI(t, "pack", "create", "Bad_Name", "--dir", dir); err == nil || !strings.Contains(err.Error(), "lowercase") {
		t.Errorf("bad name: %v", err)
	}
}
