package agent

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Durations travel as Go duration strings ("250ms").

type faultJSON struct {
	Latency   string `json:"latency,omitempty"`
	Jitter    string `json:"jitter,omitempty"`
	Bandwidth int64  `json:"bandwidth,omitempty"`
	Reset     bool   `json:"reset,omitempty"`
	Refuse    bool   `json:"refuse,omitempty"`
	Blackhole bool   `json:"blackhole,omitempty"`
}

func durString(d time.Duration) string {
	if d == 0 {
		return ""
	}
	return d.String()
}

func parseDur(s, field string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", field, err)
	}
	return d, nil
}

// MarshalJSON writes durations as strings.
func (f Fault) MarshalJSON() ([]byte, error) {
	return json.Marshal(faultJSON{durString(f.Latency), durString(f.Jitter), f.Bandwidth, f.Reset, f.Refuse, f.Blackhole})
}

// UnmarshalJSON reads durations as strings.
func (f *Fault) UnmarshalJSON(b []byte) error {
	var j faultJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	var err error
	if f.Latency, err = parseDur(j.Latency, "latency"); err != nil {
		return err
	}
	if f.Jitter, err = parseDur(j.Jitter, "jitter"); err != nil {
		return err
	}
	f.Bandwidth, f.Reset, f.Refuse, f.Blackhole = j.Bandwidth, j.Reset, j.Refuse, j.Blackhole
	return nil
}

type requestJSON struct {
	Kind     Kind   `json:"kind"`
	Target   string `json:"target"`
	Fault    *Fault `json:"fault,omitempty"`
	Action   string `json:"action,omitempty"`
	Replicas *int   `json:"replicas,omitempty"`
	Duration string `json:"duration"`
	Run      string `json:"run,omitempty"`
	Label    string `json:"label,omitempty"`
}

// MarshalJSON writes the duration as a string.
func (r Request) MarshalJSON() ([]byte, error) {
	j := requestJSON{Kind: r.Kind, Target: r.Target, Action: r.Action, Replicas: r.Replicas, Duration: durString(r.Duration), Run: r.Run, Label: r.Label}
	if !r.Fault.IsZero() {
		f := r.Fault
		j.Fault = &f
	}
	return json.Marshal(j)
}

// UnmarshalJSON reads the duration as a string.
func (r *Request) UnmarshalJSON(b []byte) error {
	var j requestJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	d, err := parseDur(j.Duration, "duration")
	if err != nil {
		return err
	}
	*r = Request{Kind: j.Kind, Target: j.Target, Action: j.Action, Replicas: j.Replicas, Duration: d, Run: j.Run, Label: j.Label}
	if j.Fault != nil {
		r.Fault = *j.Fault
	}
	return nil
}

// Status is the agent's state.
type Status struct {
	Proxies     []ProxyInfo `json:"proxies"`
	Active      []Active    `json:"active"`
	Docker      bool        `json:"docker"`
	Kubernetes  bool        `json:"kubernetes"`
	MaxDuration string      `json:"maxDuration"`
}

// Handler serves the control API. Every request needs
// "Authorization: Bearer <token>".
//
//	GET    /v1/status
//	POST   /v1/faults         a Request; answers the Active fault
//	DELETE /v1/faults/{id}    revert one fault
//	DELETE /v1/faults?run=ID  revert every fault (of one run): the kill switch
func (a *Agent) Handler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, Status{Proxies: a.Proxies(), Active: a.Active(), Docker: a.cfg.Docker != nil,
			Kubernetes: a.cfg.Kubernetes != nil, MaxDuration: a.cfg.MaxDuration.String()})
	})
	mux.HandleFunc("POST /v1/faults", func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		act, err := a.Apply(r.Context(), req)
		if err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, ErrNotAllowed) {
				code = http.StatusForbidden
			}
			writeError(w, code, err)
			return
		}
		writeJSON(w, http.StatusCreated, act)
	})
	mux.HandleFunc("DELETE /v1/faults/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Revert(r.Context(), r.PathValue("id"), "cleared"); err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /v1/faults", func(w http.ResponseWriter, r *http.Request) {
		if err := a.RevertAll(r.Context(), r.URL.Query().Get("run"), "cleared"); err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			writeError(w, http.StatusUnauthorized, errors.New("a valid agent token is required (Authorization: Bearer ...)"))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// Client talks to an agent's control API.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func (c *Client) do(ctx context.Context, method, p string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+p, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("agent %s: %w", c.BaseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("agent: %s", e.Error)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// Apply starts a fault.
func (c *Client) Apply(ctx context.Context, req Request) (*Active, error) {
	var act Active
	if err := c.do(ctx, http.MethodPost, "/v1/faults", req, &act); err != nil {
		return nil, err
	}
	return &act, nil
}

// Revert ends one fault.
func (c *Client) Revert(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/faults/"+url.PathEscape(id), nil, nil)
}

// RevertAll ends every fault of a run (all faults when run is empty).
func (c *Client) RevertAll(ctx context.Context, run string) error {
	p := "/v1/faults"
	if run != "" {
		p += "?run=" + url.QueryEscape(run)
	}
	return c.do(ctx, http.MethodDelete, p, nil, nil)
}

// Status reads the agent's state.
func (c *Client) Status(ctx context.Context) (*Status, error) {
	var s Status
	if err := c.do(ctx, http.MethodGet, "/v1/status", nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
