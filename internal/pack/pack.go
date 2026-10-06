// Package pack loads product packs (folders of journeys, stresses and
// targets for one kind of product), detects which pack fits a target, and
// installs a pack's files into a project.
package pack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Ivan825/Stampede/packs"
)

// Entry is one line of the catalogue.
type Entry struct {
	Name      string   `yaml:"name" json:"name"`
	Title     string   `yaml:"title" json:"title"`
	Status    string   `yaml:"status" json:"status"`
	Signature string   `yaml:"signature" json:"signature"`
	Drivers   []string `yaml:"drivers" json:"drivers"`
}

// Pack is a pack's manifest.
type Pack struct {
	Name         string   `yaml:"name"`
	Title        string   `yaml:"title"`
	Description  string   `yaml:"description"`
	Status       string   `yaml:"status"`
	Protocols    []string `yaml:"protocols"`
	ReferenceApp string   `yaml:"referenceApp"`
	Detect       Detect   `yaml:"detect"`
	Variables    map[string]struct {
		Description string `yaml:"description"`
	} `yaml:"variables"`

	fsys fs.FS
	dir  string
}

// Detect lists evidence that a target is this kind of product.
type Detect struct {
	Paths       []string          `yaml:"paths"`
	OpenAPITags []string          `yaml:"openapiTags"`
	HTMLMeta    []string          `yaml:"htmlMeta"`
	Headers     map[string]string `yaml:"headers"`
}

// Catalog reads the built-in catalogue.
func Catalog() ([]Entry, error) {
	b, err := fs.ReadFile(packs.FS, "catalog.yaml")
	if err != nil {
		return nil, err
	}
	var c struct {
		Packs []Entry `yaml:"packs"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return c.Packs, nil
}

// Load reads a built-in pack by name, or a pack directory on disk.
func Load(nameOrDir string) (*Pack, error) {
	var fsys fs.FS = packs.FS
	dir := nameOrDir
	if st, err := os.Stat(nameOrDir); err == nil && st.IsDir() {
		fsys, dir = os.DirFS(nameOrDir), "."
	}
	b, err := fs.ReadFile(fsys, path.Join(dir, "pack.yaml"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("pack %q not found (see stampede pack list)", nameOrDir)
		}
		return nil, err
	}
	var p Pack
	if err := yaml.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("%s/pack.yaml: %w", nameOrDir, err)
	}
	p.fsys, p.dir = fsys, dir
	return &p, nil
}

// Shipped loads every shipped built-in pack.
func Shipped() ([]*Pack, error) {
	cat, err := Catalog()
	if err != nil {
		return nil, err
	}
	var out []*Pack
	for _, e := range cat {
		if e.Status != "shipped" {
			continue
		}
		p, err := Load(e.Name)
		if err != nil {
			return nil, fmt.Errorf("catalogue lists %s as shipped: %w", e.Name, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// Files lists the pack's scenario files (journeys and stresses), relative
// to the pack root.
func (p *Pack) Files() ([]string, error) {
	var out []string
	for _, sub := range []string{"journeys", "stresses"} {
		ents, err := fs.ReadDir(p.fsys, path.Join(p.dir, sub))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, e := range ents {
			if !e.IsDir() && (strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml")) {
				out = append(out, path.Join(sub, e.Name()))
			}
		}
	}
	return out, nil
}

// ReadFile reads a file from the pack.
func (p *Pack) ReadFile(rel string) ([]byte, error) {
	return fs.ReadFile(p.fsys, path.Join(p.dir, rel))
}

// Install copies the pack into dir/<pack name>, keeping its layout so
// relative data paths keep working. Existing files are not overwritten
// unless force is set. It returns the files written.
func (p *Pack) Install(dir string, force bool) ([]string, error) {
	root := filepath.Join(dir, p.Name)
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	var written []string
	err := fs.WalkDir(p.fsys, p.dir, func(fp string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(fp, p.dir), "/")
		if rel == "" {
			return nil
		}
		dst := filepath.Join(root, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o750)
		}
		if _, err := os.Stat(dst); err == nil && !force {
			return nil
		}
		b, err := fs.ReadFile(p.fsys, fp)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			return err
		}
		written = append(written, dst)
		return nil
	})
	return written, err
}

// Evidence is what detection found about a target.
type Evidence struct {
	OpenAPI     bool
	Paths       []string
	OpenAPITags []string
	HTML        string
	Headers     http.Header
}

// Match is a pack's detection score.
type Match struct {
	Pack    *Pack
	Score   int
	Reasons []string
}

// Probe gathers evidence about a target: its OpenAPI document if it
// publishes one at a common path, response headers and the home page.
func Probe(ctx context.Context, base *url.URL, hc *http.Client) (*Evidence, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
	}
	ev := &Evidence{}
	get := func(p string) (*http.Response, []byte, error) {
		u := *base
		u.Path = strings.TrimRight(base.Path, "/") + p
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("User-Agent", "stampede-init")
		resp, err := hc.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		return resp, b, err
	}
	resp, body, err := get("/")
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", base, err)
	}
	ev.Headers = resp.Header
	if strings.Contains(resp.Header.Get("Content-Type"), "html") {
		ev.HTML = string(body)
	}
	for _, p := range []string{"/openapi.yaml", "/openapi.json", "/swagger.json", "/v3/api-docs", "/api/openapi.json", "/docs/openapi.yaml"} {
		resp, b, err := get(p)
		if err != nil || resp.StatusCode != 200 {
			continue
		}
		var doc struct {
			Paths map[string]any `json:"paths" yaml:"paths"`
			Tags  []struct {
				Name string `json:"name" yaml:"name"`
			} `json:"tags" yaml:"tags"`
		}
		if json.Unmarshal(b, &doc) != nil && yaml.Unmarshal(b, &doc) != nil {
			continue
		}
		if len(doc.Paths) == 0 {
			continue
		}
		ev.OpenAPI = true
		for p := range doc.Paths {
			ev.Paths = append(ev.Paths, p)
		}
		for _, t := range doc.Tags {
			ev.OpenAPITags = append(ev.OpenAPITags, strings.ToLower(t.Name))
		}
		sort.Strings(ev.Paths)
		break
	}
	return ev, nil
}

// Score ranks packs against the evidence, best first.
func Score(ev *Evidence, ps []*Pack) []Match {
	var out []Match
	for _, p := range ps {
		m := Match{Pack: p}
		for _, want := range p.Detect.Paths {
			for _, have := range ev.Paths {
				if have == want || strings.HasPrefix(have, want+"/") {
					m.Score += 3
					m.Reasons = append(m.Reasons, "API path "+have)
					break
				}
			}
		}
		for _, want := range p.Detect.OpenAPITags {
			for _, have := range ev.OpenAPITags {
				if have == want {
					m.Score += 2
					m.Reasons = append(m.Reasons, "API tag "+have)
				}
			}
		}
		for _, want := range p.Detect.HTMLMeta {
			if ev.HTML != "" && strings.Contains(ev.HTML, want) {
				m.Score += 3
				m.Reasons = append(m.Reasons, "page markup "+want)
			}
		}
		for k, v := range p.Detect.Headers {
			if strings.Contains(strings.ToLower(ev.Headers.Get(k)), strings.ToLower(v)) {
				m.Score += 2
				m.Reasons = append(m.Reasons, "header "+k)
			}
		}
		if m.Score > 0 {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}
