package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

func allowed(patterns []string, name string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

func call(ctx context.Context, c *http.Client, method, u string, hdr http.Header, body []byte, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var msg struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &msg)
		if msg.Message == "" {
			msg.Message = strings.TrimSpace(string(b))
		}
		return fmt.Errorf("%s %s: %s: %s", method, req.URL.Path, resp.Status, msg.Message)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// Docker acts on containers through the Docker Engine API.
type Docker struct {
	// Allowed lists container names (globs) the agent may act on.
	Allowed []string
	client  *http.Client
	base    string
}

// NewDocker talks to the Engine API at host: unix:///var/run/docker.sock
// (the default), or an http:// URL.
func NewDocker(host string, allowedNames []string) (*Docker, error) {
	if len(allowedNames) == 0 {
		return nil, errors.New("name the containers the agent may touch with --allow-container")
	}
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	d := &Docker{Allowed: allowedNames}
	switch {
	case strings.HasPrefix(host, "unix://"):
		sock := strings.TrimPrefix(host, "unix://")
		d.client = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dl net.Dialer
			return dl.DialContext(ctx, "unix", sock)
		}}}
		d.base = "http://docker/v1.43"
	case strings.HasPrefix(host, "http://"):
		d.client = http.DefaultClient
		d.base = strings.TrimRight(host, "/") + "/v1.43"
	default:
		return nil, fmt.Errorf("docker host %q: use unix:// or http://", host)
	}
	return d, nil
}

// Act pauses, stops, kills or restarts a container and returns how to undo
// it: unpause, or start it again. A restart needs no undo.
func (d *Docker) Act(ctx context.Context, name, action string) (func(context.Context) error, error) {
	if !allowed(d.Allowed, name) {
		return nil, fmt.Errorf("container %q is %w: add it with --allow-container", name, ErrNotAllowed)
	}
	var info struct {
		State struct {
			Running bool `json:"Running"`
			Paused  bool `json:"Paused"`
		} `json:"State"`
	}
	c := d.base + "/containers/" + url.PathEscape(name)
	if err := call(ctx, d.client, http.MethodGet, c+"/json", nil, nil, &info); err != nil {
		return nil, err
	}
	if !info.State.Running || info.State.Paused {
		return nil, fmt.Errorf("container %q is not running", name)
	}
	post := func(op string) func(context.Context) error {
		return func(ctx context.Context) error { return call(ctx, d.client, http.MethodPost, c+"/"+op, nil, nil, nil) }
	}
	var do, undo func(context.Context) error
	switch action {
	case "pause":
		do, undo = post("pause"), post("unpause")
	case "stop":
		do, undo = post("stop?t=10"), post("start")
	case "kill":
		do, undo = post("kill"), post("start")
	case "restart":
		do, undo = post("restart?t=10"), func(context.Context) error { return nil }
	default:
		return nil, fmt.Errorf("unknown container action %q: use pause, stop, kill or restart", action)
	}
	if err := do(ctx); err != nil {
		return nil, err
	}
	return undo, nil
}

// Kubernetes scales deployments through the API server, with the pod's
// service account when running in a cluster.
type Kubernetes struct {
	// Allowed lists deployments (namespace/name globs) the agent may scale.
	Allowed []string
	client  *http.Client
	base    string
	token   string
}

// NewKubernetes uses the in-cluster service account, or base and token
// when given (tests, or a kubectl proxy at http://127.0.0.1:8001).
func NewKubernetes(base, token string, allowedDeployments []string) (*Kubernetes, error) {
	if len(allowedDeployments) == 0 {
		return nil, errors.New("name the deployments the agent may scale with --allow-deployment namespace/name")
	}
	k := &Kubernetes{Allowed: allowedDeployments, base: strings.TrimRight(base, "/"), token: token, client: http.DefaultClient}
	if k.base != "" {
		return k, nil
	}
	const sa = "/var/run/secrets/kubernetes.io/serviceaccount/"
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" {
		return nil, errors.New("not running in a Kubernetes cluster: pass --kubernetes-api (for example a kubectl proxy)")
	}
	tok, err := os.ReadFile(sa + "token")
	if err != nil {
		return nil, err
	}
	ca, err := os.ReadFile(sa + "ca.crt")
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	k.client = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	k.base = "https://" + net.JoinHostPort(host, port)
	k.token = strings.TrimSpace(string(tok))
	return k, nil
}

// Scale sets a deployment's replicas and returns how to restore them.
func (k *Kubernetes) Scale(ctx context.Context, target string, replicas int) (func(context.Context) error, error) {
	ns, name, ok := strings.Cut(target, "/")
	if !ok || ns == "" || name == "" {
		return nil, fmt.Errorf("deployment %q: use namespace/name", target)
	}
	if !allowed(k.Allowed, target) {
		return nil, fmt.Errorf("deployment %q is %w: add it with --allow-deployment", target, ErrNotAllowed)
	}
	u := k.base + "/apis/apps/v1/namespaces/" + url.PathEscape(ns) + "/deployments/" + url.PathEscape(name) + "/scale"
	hdr := http.Header{}
	if k.token != "" {
		hdr.Set("Authorization", "Bearer "+k.token)
	}
	var cur struct {
		Spec struct {
			Replicas int `json:"replicas"`
		} `json:"spec"`
	}
	if err := call(ctx, k.client, http.MethodGet, u, hdr, nil, &cur); err != nil {
		return nil, err
	}
	set := func(ctx context.Context, n int) error {
		h := hdr.Clone()
		h.Set("Content-Type", "application/merge-patch+json")
		return call(ctx, k.client, http.MethodPatch, u, h, fmt.Appendf(nil, `{"spec":{"replicas":%d}}`, n), nil)
	}
	if err := set(ctx, replicas); err != nil {
		return nil, err
	}
	old := cur.Spec.Replicas
	return func(ctx context.Context) error { return set(ctx, old) }, nil
}
