package stampede

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"sigs.k8s.io/yaml"
)

// ResolveProject finds a project by ID, slug or name (case-insensitive), and
// creates one named ref when none matches.
func (c *Client) ResolveProject(ctx context.Context, ref string) (*Project, error) {
	projects, err := c.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for i := range projects {
		p := &projects[i]
		if p.ID == ref || p.Slug == ref || strings.EqualFold(p.Name, ref) {
			return p, nil
		}
	}
	return c.CreateProject(ctx, ref)
}

// ResolveTarget finds the project's target with this base URL, and registers
// one when none matches.
func (c *Client) ResolveTarget(ctx context.Context, projectID, baseURL string) (*Target, error) {
	want := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	targets, err := c.ListTargets(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for i := range targets {
		if strings.TrimRight(targets[i].BaseURL, "/") == want {
			return &targets[i], nil
		}
	}
	u, err := url.Parse(want)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("target URL %q is not an absolute URL", baseURL)
	}
	return c.CreateTarget(ctx, projectID, u.Host, want)
}

// ResolveScenario returns the scenario ID and version to run. A ref selects a
// stored scenario by name or ID (version 0 means latest). Inline YAML is
// saved under its metadata.name: created when new, or as a new version when
// the stored latest version differs.
func (c *Client) ResolveScenario(ctx context.Context, projectID, ref string, version int32, inline, message string) (string, int32, error) {
	scenarios, err := c.ListScenarios(ctx, projectID)
	if err != nil {
		return "", 0, err
	}
	if inline == "" {
		for _, s := range scenarios {
			if s.ID == ref || s.Name == ref {
				if version == 0 {
					version = s.LatestVersion.Version
				}
				return s.ID, version, nil
			}
		}
		return "", 0, &APIError{Status: http.StatusNotFound, Code: "not_found", Message: fmt.Sprintf("no scenario %q in the project", ref)}
	}
	name, err := ScenarioName(inline)
	if err != nil {
		return "", 0, err
	}
	for _, s := range scenarios {
		if s.Name != name {
			continue
		}
		if s.LatestVersion.YAML == inline {
			return s.ID, s.LatestVersion.Version, nil
		}
		v, err := c.CreateScenarioVersion(ctx, s.ID, inline, message)
		if err != nil {
			return "", 0, err
		}
		return s.ID, v.Version, nil
	}
	s, err := c.CreateScenario(ctx, projectID, inline, message)
	if err != nil {
		return "", 0, err
	}
	return s.ID, s.LatestVersion.Version, nil
}

// ScenarioName reads metadata.name from a scenario document.
func ScenarioName(doc string) (string, error) {
	var s struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(doc), &s); err != nil {
		return "", fmt.Errorf("inline scenario: %w", err)
	}
	if s.Metadata.Name == "" {
		return "", errors.New("inline scenario has no metadata.name")
	}
	return s.Metadata.Name, nil
}

// FindRunByNote returns the most recent run whose note contains marker.
func (c *Client) FindRunByNote(ctx context.Context, projectID, marker string) (*Run, error) {
	runs, err := c.ListRuns(ctx, projectID, 50)
	if err != nil {
		return nil, err
	}
	for i := range runs {
		if strings.Contains(runs[i].Note, marker) {
			return &runs[i], nil
		}
	}
	return nil, nil
}

// ConnectedWorkers counts workers that are connected and not lost.
func (c *Client) ConnectedWorkers(ctx context.Context) (int, error) {
	ws, err := c.ListWorkers(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, w := range ws {
		if w.Status != "lost" {
			n++
		}
	}
	return n, nil
}
