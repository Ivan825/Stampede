package pack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/Ivan825/Stampede/internal/scenario"
)

func TestCatalog(t *testing.T) {
	cat, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(cat) != 20 {
		t.Errorf("catalogue lists %d packs, the plan has 20", len(cat))
	}
	seen := map[string]bool{}
	for _, e := range cat {
		if seen[e.Name] {
			t.Errorf("duplicate %s", e.Name)
		}
		seen[e.Name] = true
		if e.Status != "shipped" && e.Status != "planned" {
			t.Errorf("%s: status %q", e.Name, e.Status)
		}
	}
	shipped, err := Shipped()
	if err != nil || len(shipped) == 0 {
		t.Fatalf("shipped packs: %v", err)
	}
}

// Every scenario in every shipped pack must parse and validate, with its
// data files present once installed.
func TestShippedPacksAreValid(t *testing.T) {
	shipped, _ := Shipped()
	for _, p := range shipped {
		if p.ReferenceApp == "" {
			t.Errorf("%s: a shipped pack needs a reference app", p.Name)
		}
		dir := t.TempDir()
		if _, err := p.Install(dir, false); err != nil {
			t.Fatal(err)
		}
		files, err := p.Files()
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: no scenario files (%v)", p.Name, err)
		}
		for _, f := range files {
			s, err := scenario.LoadFile(filepath.Join(dir, p.Name, filepath.FromSlash(f)))
			if err != nil {
				t.Errorf("%s/%s: %v", p.Name, f, err)
				continue
			}
			for name, fd := range s.Data {
				if fd.CSV != "" {
					if _, err := os.Stat(fd.CSV); err != nil {
						t.Errorf("%s/%s: data.%s file missing after install", p.Name, f, name)
					}
				}
			}
		}
	}
}

func TestDetectShopLab(t *testing.T) {
	spec, err := os.ReadFile("../../examples/shoplab/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/openapi.yaml" {
			w.Write(spec)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"ShopLab"}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	ev, err := Probe(context.Background(), u, nil)
	if err != nil || !ev.OpenAPI {
		t.Fatalf("probe: %v %+v", err, ev)
	}
	shipped, _ := Shipped()
	m := Score(ev, shipped)
	if len(m) == 0 || m[0].Pack.Name != "ecommerce" {
		t.Fatalf("matches %+v", m)
	}

	// A target with nothing shop-like matches no pack.
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer plain.Close()
	u2, _ := url.Parse(plain.URL)
	ev2, _ := Probe(context.Background(), u2, nil)
	if got := Score(ev2, shipped); len(got) != 0 {
		t.Errorf("unexpected match %+v", got)
	}
}
