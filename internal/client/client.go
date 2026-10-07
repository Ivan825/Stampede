// Package client is a small client for the Stampede REST API, used by the
// CLI and the terminal UI. It speaks the same public API as the web UI.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Ivan825/Stampede/internal/api/gen"
)

// Client calls a Stampede server.
type Client struct {
	Server string
	Token  string
	// Project is the default project for commands given no --project
	// (stampede projects use, or STAMPEDE_PROJECT).
	Project string
	HTTP    *http.Client
}

// Config is what `stampede login` stores.
type Config struct {
	Server  string `yaml:"server"`
	Token   string `yaml:"token"`
	Project string `yaml:"project,omitempty"`
}

// ConfigPath is ~/.config/stampede/config.yaml (or the OS equivalent).
func ConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "stampede", "config.yaml"), nil
}

// ReadConfigFile reads the stored config without environment overrides.
func ReadConfigFile() (Config, error) {
	var c Config
	if p, err := ConfigPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			if err := yaml.Unmarshal(b, &c); err != nil {
				return c, fmt.Errorf("%s: %w", p, err)
			}
		}
	}
	return c, nil
}

// LoadConfig reads the stored config; env vars STAMPEDE_SERVER,
// STAMPEDE_TOKEN and STAMPEDE_PROJECT override it.
func LoadConfig() (Config, error) {
	c, err := ReadConfigFile()
	if err != nil {
		return c, err
	}
	if v := os.Getenv("STAMPEDE_SERVER"); v != "" {
		c.Server = v
	}
	if v := os.Getenv("STAMPEDE_TOKEN"); v != "" {
		c.Token = v
	}
	if v := os.Getenv("STAMPEDE_PROJECT"); v != "" {
		c.Project = v
	}
	return c, nil
}

// SaveConfig writes the config with owner-only permissions (it holds a token).
func SaveConfig(c Config) (string, error) {
	p, err := ConfigPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return "", err
	}
	return p, os.WriteFile(p, b, 0o600)
}

// New builds a client from the stored config.
func New() (*Client, error) {
	c, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if c.Server == "" || c.Token == "" {
		return nil, errors.New("not signed in: run `stampede login --server http://localhost:8080` or set STAMPEDE_SERVER and STAMPEDE_TOKEN")
	}
	return &Client{Server: strings.TrimRight(c.Server, "/"), Token: c.Token, Project: c.Project, HTTP: &http.Client{Timeout: 60 * time.Second}}, nil
}

// APIError is an error response from the server.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details []string
}

func (e *APIError) Error() string {
	msg := e.Message
	if len(e.Details) > 0 {
		msg += ":\n  - " + strings.Join(e.Details, "\n  - ")
	}
	return msg
}

// Do sends a request with a JSON body and decodes a JSON response into out.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Server+"/api/v1"+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	} else {
		req.Header.Set("X-Stampede-CSRF", "1")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error struct {
				Code    string   `json:"code"`
				Message string   `json:"message"`
				Details []string `json:"details"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Error.Message == "" {
			e.Error.Message = fmt.Sprintf("%s %s: HTTP %d", method, path, resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Code: e.Error.Code, Message: e.Error.Message, Details: e.Error.Details}
	}
	if out != nil && len(b) > 0 {
		if s, ok := out.(*[]byte); ok {
			*s = b
			return nil
		}
		// Decode into a zero value: unmarshalling into a struct or map
		// that holds the request's data would keep fields and keys the
		// response leaves out.
		if v := reflect.ValueOf(out); v.Kind() == reflect.Pointer && !v.IsNil() {
			v.Elem().SetZero()
		}
		return json.Unmarshal(b, out)
	}
	return nil
}

// Login signs in with a password and creates an API token for this CLI.
func Login(ctx context.Context, server, email, password, tokenName string) (string, gen.Me, error) {
	tok, me, err := PasswordToken(ctx, server, email, password, map[string]any{"name": tokenName})
	return tok.Secret, me, err
}

// PasswordToken signs in with a password and creates an API token from
// body (a TokenCreate). API tokens cannot create tokens, so this is how
// the CLI makes them.
func PasswordToken(ctx context.Context, server, email, password string, body map[string]any) (gen.TokenCreated, gen.Me, error) {
	c := sessionClient(server)
	var sess gen.Session
	if err := c.Do(ctx, "POST", "/auth/login", map[string]string{"email": email, "password": password}, &sess); err != nil {
		return gen.TokenCreated{}, gen.Me{}, err
	}
	var tok gen.TokenCreated
	if err := c.Do(ctx, "POST", "/tokens", body, &tok); err != nil {
		return gen.TokenCreated{}, gen.Me{}, err
	}
	return tok, sess.User, nil
}

// Setup creates the first organisation and owner account on a new
// server, then an API token for this CLI.
func Setup(ctx context.Context, server, organisation, name, email, password, tokenName string) (string, gen.Me, error) {
	c := sessionClient(server)
	var sess gen.Session
	body := map[string]string{"organisation": organisation, "name": name, "email": email, "password": password}
	if err := c.Do(ctx, "POST", "/setup", body, &sess); err != nil {
		return "", gen.Me{}, err
	}
	var tok gen.TokenCreated
	if err := c.Do(ctx, "POST", "/tokens", map[string]any{"name": tokenName}, &tok); err != nil {
		return "", gen.Me{}, err
	}
	return tok.Secret, sess.User, nil
}

// sessionClient keeps the session cookie between requests.
func sessionClient(server string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{Server: strings.TrimRight(server, "/"), HTTP: &http.Client{Timeout: 30 * time.Second, Jar: jar}}
}

// Projects lists projects.
func (c *Client) Projects(ctx context.Context) ([]gen.Project, error) {
	var out []gen.Project
	return out, c.Do(ctx, "GET", "/projects", nil, &out)
}

// FindProject resolves a project by id, slug or name.
func (c *Client) FindProject(ctx context.Context, ref string) (gen.Project, error) {
	ps, err := c.Projects(ctx)
	if err != nil {
		return gen.Project{}, err
	}
	if ref == "" {
		ref = c.Project
	}
	if ref == "" && len(ps) == 1 {
		return ps[0], nil
	}
	for _, p := range ps {
		if p.Id.String() == ref || p.Slug == ref || strings.EqualFold(p.Name, ref) {
			return p, nil
		}
	}
	if ref == "" && len(ps) == 0 {
		return gen.Project{}, errors.New("no projects yet; create one with stampede projects create")
	}
	if ref == "" {
		return gen.Project{}, errors.New("several projects exist; choose one with --project or stampede projects use")
	}
	return gen.Project{}, fmt.Errorf("project %q not found", ref)
}

// FindScenario resolves a scenario in a project by id or name.
func (c *Client) FindScenario(ctx context.Context, project, ref string) (gen.Scenario, error) {
	var ss []gen.Scenario
	if err := c.Do(ctx, "GET", "/projects/"+project+"/scenarios", nil, &ss); err != nil {
		return gen.Scenario{}, err
	}
	if ref == "" && len(ss) == 1 {
		return ss[0], nil
	}
	for _, s := range ss {
		if s.Id.String() == ref || s.Name == ref {
			return s, nil
		}
	}
	return gen.Scenario{}, fmt.Errorf("scenario %q not found in the project", ref)
}

// FindTarget resolves a target in a project by id, name or base URL.
func (c *Client) FindTarget(ctx context.Context, project, ref string) (gen.Target, error) {
	var ts []gen.Target
	if err := c.Do(ctx, "GET", "/projects/"+project+"/targets", nil, &ts); err != nil {
		return gen.Target{}, err
	}
	if ref == "" && len(ts) == 1 {
		return ts[0], nil
	}
	for _, t := range ts {
		if t.Id.String() == ref || t.Name == ref || t.BaseURL == strings.TrimRight(ref, "/") {
			return t, nil
		}
	}
	if ref == "" {
		return gen.Target{}, errors.New("choose a target with --target")
	}
	return gen.Target{}, fmt.Errorf("target %q not found in the project", ref)
}

// Event is one server-sent event from a live run.
type Event struct {
	Type string
	Data []byte
}

// Follow streams a run's live events until it ends or ctx is done.
func (c *Client) Follow(ctx context.Context, runID string, fn func(Event)) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.Server+"/api/v1/runs/"+url.PathEscape(runID)+"/live", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "text/event-stream")
	hc := *c.HTTP
	hc.Timeout = 0
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("live stream: HTTP %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var ev Event
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			ev.Type = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			ev.Data = []byte(strings.TrimPrefix(line, "data: "))
		case line == "" && ev.Type != "":
			fn(ev)
			if ev.Type == "end" {
				return nil
			}
			ev = Event{}
		}
	}
	return sc.Err()
}
