// Package stampede is a small client for the parts of the Stampede REST API
// (/api/v1) the operator uses.
package stampede

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one Stampede server with an API token.
type Client struct {
	BaseURL string // http://host:8080, without /api/v1
	Token   string // stp_...
	HTTP    *http.Client
}

// New returns a client with a 30-second timeout.
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// APIError is an error response from the server.
type APIError struct {
	Status  int
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Details []string `json:"details"`
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("stampede API %d %s: %s", e.Status, e.Code, e.Message)
	if len(e.Details) > 0 {
		msg += " (" + strings.Join(e.Details, "; ") + ")"
	}
	return msg
}

// IsStatus reports whether err is an APIError with the given HTTP status.
func IsStatus(err error, status int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == status
}

// Permanent reports whether retrying the same request cannot succeed
// (4xx other than 408, 409 and 429).
func Permanent(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.Status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests:
		return false
	}
	return ae.Status >= 400 && ae.Status < 500
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/api/v1"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var env struct {
			Error APIError `json:"error"`
		}
		_ = json.Unmarshal(data, &env)
		env.Error.Status = resp.StatusCode
		if env.Error.Message == "" {
			env.Error.Message = strings.TrimSpace(string(data))
		}
		return &env.Error
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Project is a Stampede project.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Target is a system under test registered in a project.
type Target struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BaseURL  string `json:"baseURL"`
	Private  bool   `json:"private"`
	Verified bool   `json:"verified"`
}

// ScenarioVersion is one saved version of a scenario.
type ScenarioVersion struct {
	ScenarioID string `json:"scenarioId"`
	Version    int32  `json:"version"`
	YAML       string `json:"yaml"`
}

// Scenario is a stored scenario with its latest version.
type Scenario struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	LatestVersion ScenarioVersion `json:"latestVersion"`
}

// Overrides change a run's load.
type Overrides struct {
	Shape    string `json:"shape,omitempty"`
	Mode     string `json:"mode,omitempty"`
	VUs      int32  `json:"vus,omitempty"`
	Rate     string `json:"rate,omitempty"`
	Duration string `json:"duration,omitempty"`
	Start    string `json:"start,omitempty"`
	Max      string `json:"max,omitempty"`
}

// RunCreate is the body of POST /projects/{id}/runs.
type RunCreate struct {
	ScenarioID string            `json:"scenarioId"`
	Version    int32             `json:"version,omitempty"`
	TargetID   string            `json:"targetId"`
	Overrides  *Overrides        `json:"overrides,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Workers    int32             `json:"workers,omitempty"`
	Note       string            `json:"note,omitempty"`
}

// RunSummary is the headline result of a finished run.
type RunSummary struct {
	Requests  int64   `json:"requests"`
	ErrorRate float64 `json:"errorRate"`
	RPS       float64 `json:"rps"`
	P95       float64 `json:"p95"`
	P99       float64 `json:"p99"`
}

// Run is a Stampede run.
type Run struct {
	ID              string      `json:"id"`
	ProjectID       string      `json:"projectId"`
	ScenarioID      string      `json:"scenarioId"`
	ScenarioVersion int32       `json:"scenarioVersion"`
	Status          string      `json:"status"`
	Verdict         *string     `json:"verdict"`
	StopReason      *string     `json:"stopReason"`
	Error           *string     `json:"error"`
	Workers         int32       `json:"workers"`
	Note            string      `json:"note"`
	Summary         *RunSummary `json:"summary"`
}

// Terminal reports whether the run has finished.
func (r *Run) Terminal() bool {
	switch r.Status {
	case "completed", "aborted", "failed":
		return true
	}
	return false
}

// Worker is a connected worker.
type Worker struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Region string            `json:"region"`
	Labels map[string]string `json:"labels"`
	Status string            `json:"status"`
	// RunID is the run the worker is busy with, if any.
	RunID *string `json:"runId,omitempty"`
}

// ListProjects returns every project the token can see.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	return out, c.do(ctx, http.MethodGet, "/projects", nil, &out)
}

// CreateProject creates a project.
func (c *Client) CreateProject(ctx context.Context, name string) (*Project, error) {
	var out Project
	return &out, c.do(ctx, http.MethodPost, "/projects", map[string]string{"name": name}, &out)
}

// ListTargets returns a project's targets.
func (c *Client) ListTargets(ctx context.Context, projectID string) ([]Target, error) {
	var out []Target
	return out, c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/targets", nil, &out)
}

// CreateTarget registers a target.
func (c *Client) CreateTarget(ctx context.Context, projectID, name, baseURL string) (*Target, error) {
	var out Target
	in := map[string]string{"name": name, "baseURL": baseURL}
	return &out, c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/targets", in, &out)
}

// ListScenarios returns a project's scenarios.
func (c *Client) ListScenarios(ctx context.Context, projectID string) ([]Scenario, error) {
	var out []Scenario
	return out, c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/scenarios", nil, &out)
}

// CreateScenario stores a new scenario (version 1).
func (c *Client) CreateScenario(ctx context.Context, projectID, yaml, message string) (*Scenario, error) {
	var out Scenario
	in := map[string]string{"yaml": yaml, "message": message}
	return &out, c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/scenarios", in, &out)
}

// CreateScenarioVersion saves a new version of a scenario.
func (c *Client) CreateScenarioVersion(ctx context.Context, scenarioID, yaml, message string) (*ScenarioVersion, error) {
	var out ScenarioVersion
	in := map[string]string{"yaml": yaml, "message": message}
	return &out, c.do(ctx, http.MethodPost, "/scenarios/"+url.PathEscape(scenarioID)+"/versions", in, &out)
}

// ListRuns returns a project's most recent runs.
func (c *Client) ListRuns(ctx context.Context, projectID string, limit int) ([]Run, error) {
	var out []Run
	path := fmt.Sprintf("/projects/%s/runs?limit=%d", url.PathEscape(projectID), limit)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// CreateRun starts a run.
func (c *Client) CreateRun(ctx context.Context, projectID string, in RunCreate) (*Run, error) {
	var out Run
	return &out, c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/runs", in, &out)
}

// GetRun reads a run.
func (c *Client) GetRun(ctx context.Context, runID string) (*Run, error) {
	var out Run
	return &out, c.do(ctx, http.MethodGet, "/runs/"+url.PathEscape(runID), nil, &out)
}

// StopRun asks a run to stop gracefully.
func (c *Client) StopRun(ctx context.Context, runID string) error {
	return c.do(ctx, http.MethodPost, "/runs/"+url.PathEscape(runID)+"/stop", nil, nil)
}

// ListWorkers returns the connected workers.
func (c *Client) ListWorkers(ctx context.Context) ([]Worker, error) {
	var out []Worker
	return out, c.do(ctx, http.MethodGet, "/workers", nil, &out)
}
