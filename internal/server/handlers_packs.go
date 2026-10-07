package server

import (
	"context"
	"path"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/pack"
)

// ListPacks lists the catalogue of built-in packs, shipped and planned.
func (h *handlers) ListPacks(ctx context.Context, _ gen.ListPacksRequestObject) (gen.ListPacksResponseObject, error) {
	if _, err := need(ctx, auth.PermView); err != nil {
		return nil, err
	}
	cat, err := pack.Catalog()
	if err != nil {
		return nil, err
	}
	out := gen.ListPacks200JSONResponse{}
	for _, e := range cat {
		out = append(out, gen.PackEntry{
			Name: e.Name, Title: e.Title, Status: e.Status, Signature: e.Signature,
			Drivers: append([]string{}, e.Drivers...),
		})
	}
	return out, nil
}

// GetPack describes a shipped built-in pack and its scenario files. Only
// names in the catalogue are looked up, never a directory on disk.
func (h *handlers) GetPack(ctx context.Context, req gen.GetPackRequestObject) (gen.GetPackResponseObject, error) {
	if _, err := need(ctx, auth.PermView); err != nil {
		return nil, err
	}
	cat, err := pack.Catalog()
	if err != nil {
		return nil, err
	}
	shipped := false
	for _, e := range cat {
		if e.Name == req.PackName && e.Status == "shipped" {
			shipped = true
		}
	}
	if !shipped {
		return nil, errNotFound("pack")
	}
	p, err := pack.Load(req.PackName)
	if err != nil {
		return nil, err
	}
	out := gen.PackDetail{
		Name: p.Name, Title: p.Title, Description: strings.TrimSpace(p.Description), Status: p.Status,
		Protocols: append([]string{}, p.Protocols...), Files: []gen.PackFile{},
	}
	if p.ReferenceApp != "" {
		out.ReferenceApp = ptr(p.ReferenceApp)
	}
	names := make([]string, 0, len(p.Variables))
	for k := range p.Variables {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		out.Variables = append(out.Variables, struct {
			Description string `json:"description"`
			Name        string `json:"name"`
		}{Description: p.Variables[k].Description, Name: k})
	}
	if out.Variables == nil {
		out.Variables = []struct {
			Description string `json:"description"`
			Name        string `json:"name"`
		}{}
	}
	files, err := p.Files()
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		b, err := p.ReadFile(f)
		if err != nil {
			return nil, err
		}
		out.Files = append(out.Files, packFileOf(f, b))
	}
	return gen.GetPack200JSONResponse(out), nil
}

// packFileOf summarises one scenario file of a pack: its name, journeys
// and load shape, and the comment that opens it when the scenario has no
// description.
func packFileOf(rel string, b []byte) gen.PackFile {
	var doc struct {
		Metadata struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		} `yaml:"metadata"`
		Journeys []struct {
			Name string `yaml:"name"`
		} `yaml:"journeys"`
		Load struct {
			Shape string `yaml:"shape"`
		} `yaml:"load"`
	}
	_ = yaml.Unmarshal(b, &doc) // a file that does not parse still lists by path
	kind := gen.PackFileJourney
	if strings.HasPrefix(rel, "stresses/") {
		kind = gen.PackFileStress
	}
	f := gen.PackFile{
		Path: rel, Kind: kind, Scenario: doc.Metadata.Name, Journeys: []string{}, Yaml: string(b),
	}
	if f.Scenario == "" {
		f.Scenario = strings.TrimSuffix(path.Base(rel), path.Ext(rel))
	}
	for _, j := range doc.Journeys {
		if j.Name != "" {
			f.Journeys = append(f.Journeys, j.Name)
		}
	}
	if doc.Load.Shape != "" {
		f.Shape = ptr(doc.Load.Shape)
	}
	desc := strings.TrimSpace(doc.Metadata.Description)
	if desc == "" {
		desc = leadingComment(string(b))
	}
	if desc != "" {
		f.Description = ptr(desc)
	}
	return f
}

// leadingComment joins the comment lines a YAML file starts with.
func leadingComment(s string) string {
	var parts []string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		c, ok := strings.CutPrefix(t, "#")
		if !ok {
			break
		}
		if c = strings.TrimSpace(c); c != "" {
			parts = append(parts, c)
		}
	}
	return strings.Join(parts, " ")
}
