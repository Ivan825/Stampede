package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Kind is what an action does.
type Kind string

const (
	KindProxy      Kind = "proxy"      // a Fault on a proxy
	KindContainer  Kind = "container"  // pause, stop or restart a Docker container
	KindDeployment Kind = "deployment" // scale a Kubernetes deployment
)

// Request asks for one fault for a while.
type Request struct {
	Kind Kind `json:"kind"`
	// Target is a proxy name, a container name or id, or a deployment as
	// namespace/name.
	Target string `json:"target"`
	// Fault applies to proxies.
	Fault Fault `json:"fault,omitempty"`
	// Action applies to containers: pause, stop or restart.
	Action string `json:"action,omitempty"`
	// Replicas applies to deployments.
	Replicas *int `json:"replicas,omitempty"`
	// Duration is how long the fault lasts; required, at most MaxDuration.
	Duration time.Duration `json:"duration"`
	// Run and Label say who asked, for the audit log.
	Run   string `json:"run,omitempty"`
	Label string `json:"label,omitempty"`
}

// Active is a fault in force.
type Active struct {
	ID      string    `json:"id"`
	Request Request   `json:"request"`
	Started time.Time `json:"started"`
	Ends    time.Time `json:"ends"`

	revert func(context.Context) error
	timer  *time.Timer
}

// Config configures an Agent.
type Config struct {
	Proxies []*Proxy
	// Docker, when set, allows container actions.
	Docker *Docker
	// Kubernetes, when set, allows deployment scaling.
	Kubernetes *Kubernetes
	// MaxDuration caps every fault (default 30 minutes), so a forgotten
	// fault always ends.
	MaxDuration time.Duration
	// Audit receives one record per action and revert.
	Audit  *slog.Logger
	Logger *slog.Logger
}

// Agent applies and reverts faults.
type Agent struct {
	cfg     Config
	proxies map[string]*Proxy

	mu     sync.Mutex
	active map[string]*Active
	// byTarget makes a new fault on a target replace the old one.
	byTarget map[string]string
}

// New returns an agent.
func New(cfg Config) *Agent {
	if cfg.MaxDuration <= 0 {
		cfg.MaxDuration = 30 * time.Minute
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Audit == nil {
		cfg.Audit = cfg.Logger
	}
	a := &Agent{cfg: cfg, proxies: map[string]*Proxy{}, active: map[string]*Active{}, byTarget: map[string]string{}}
	for _, p := range cfg.Proxies {
		a.proxies[p.Name] = p
	}
	return a
}

// ErrNotAllowed means the agent was not started with permission for the
// action.
var ErrNotAllowed = errors.New("not allowed")

// Apply starts a fault. It replaces any fault already active on the same
// target, and reverts by itself after the duration.
func (a *Agent) Apply(ctx context.Context, req Request) (*Active, error) {
	if req.Duration <= 0 {
		return nil, errors.New("duration is required: every fault must end")
	}
	if req.Duration > a.cfg.MaxDuration {
		return nil, fmt.Errorf("duration %s is over this agent's limit of %s (--max-duration)", req.Duration, a.cfg.MaxDuration)
	}
	key := string(req.Kind) + "/" + req.Target
	a.mu.Lock()
	if old := a.byTarget[key]; old != "" {
		a.mu.Unlock()
		if err := a.Revert(ctx, old, "replaced"); err != nil {
			return nil, err
		}
		a.mu.Lock()
	}
	a.mu.Unlock()

	var revert func(context.Context) error
	switch req.Kind {
	case KindProxy:
		p := a.proxies[req.Target]
		if p == nil {
			return nil, fmt.Errorf("no proxy named %q (have %s)", req.Target, a.proxyNames())
		}
		if req.Fault.IsZero() {
			return nil, errors.New("the fault changes nothing: set latency, jitter, bandwidth, reset, refuse or blackhole")
		}
		if req.Fault.Latency < 0 || req.Fault.Jitter < 0 || req.Fault.Bandwidth < 0 {
			return nil, errors.New("latency, jitter and bandwidth must not be negative")
		}
		p.SetFault(req.Fault)
		revert = func(context.Context) error { p.SetFault(Fault{}); return nil }
	case KindContainer:
		if a.cfg.Docker == nil {
			return nil, fmt.Errorf("container actions are %w: start the agent with --allow-docker", ErrNotAllowed)
		}
		r, err := a.cfg.Docker.Act(ctx, req.Target, req.Action)
		if err != nil {
			return nil, err
		}
		revert = r
	case KindDeployment:
		if a.cfg.Kubernetes == nil {
			return nil, fmt.Errorf("deployment scaling is %w: start the agent with --allow-kubernetes", ErrNotAllowed)
		}
		if req.Replicas == nil || *req.Replicas < 0 {
			return nil, errors.New("replicas is required and must not be negative")
		}
		r, err := a.cfg.Kubernetes.Scale(ctx, req.Target, *req.Replicas)
		if err != nil {
			return nil, err
		}
		revert = r
	default:
		return nil, fmt.Errorf("unknown kind %q: use proxy, container or deployment", req.Kind)
	}

	var idb [6]byte
	_, _ = rand.Read(idb[:])
	now := time.Now()
	act := &Active{ID: hex.EncodeToString(idb[:]), Request: req, Started: now, Ends: now.Add(req.Duration), revert: revert}
	a.mu.Lock()
	a.active[act.ID] = act
	a.byTarget[key] = act.ID
	act.timer = time.AfterFunc(req.Duration, func() {
		_ = a.Revert(context.Background(), act.ID, "duration ended")
	})
	a.mu.Unlock()
	a.cfg.Audit.Info("fault applied", auditAttrs(act)...)
	return act, nil
}

func auditAttrs(act *Active) []any {
	r := act.Request
	attrs := []any{"id", act.ID, "kind", r.Kind, "target", r.Target, "duration", r.Duration.String(), "run", r.Run, "label", r.Label}
	switch r.Kind {
	case KindProxy:
		attrs = append(attrs, "latency", r.Fault.Latency.String(), "jitter", r.Fault.Jitter.String(), "bandwidth", r.Fault.Bandwidth,
			"reset", r.Fault.Reset, "refuse", r.Fault.Refuse, "blackhole", r.Fault.Blackhole)
	case KindContainer:
		attrs = append(attrs, "action", r.Action)
	case KindDeployment:
		attrs = append(attrs, "replicas", *r.Replicas)
	}
	return attrs
}

// Revert ends one fault.
func (a *Agent) Revert(ctx context.Context, id, why string) error {
	a.mu.Lock()
	act := a.active[id]
	if act == nil {
		a.mu.Unlock()
		return nil
	}
	delete(a.active, id)
	key := string(act.Request.Kind) + "/" + act.Request.Target
	if a.byTarget[key] == id {
		delete(a.byTarget, key)
	}
	act.timer.Stop()
	a.mu.Unlock()
	err := act.revert(ctx)
	attrs := append(auditAttrs(act), "why", why)
	if err != nil {
		a.cfg.Audit.Error("fault revert failed", append(attrs, "error", err.Error())...)
		return fmt.Errorf("revert %s on %s: %w", act.Request.Kind, act.Request.Target, err)
	}
	a.cfg.Audit.Info("fault reverted", attrs...)
	return nil
}

// RevertAll ends every fault, or only those of one run when run is set.
// It is the kill switch.
func (a *Agent) RevertAll(ctx context.Context, run, why string) error {
	var errs []error
	for _, act := range a.Active() {
		if run == "" || act.Request.Run == run {
			errs = append(errs, a.Revert(ctx, act.ID, why))
		}
	}
	return errors.Join(errs...)
}

// Active lists faults in force, oldest first.
func (a *Agent) Active() []Active {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Active, 0, len(a.active))
	for _, act := range a.active {
		c := *act
		c.revert, c.timer = nil, nil
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

// ProxyInfo describes a proxy.
type ProxyInfo struct {
	Name     string `json:"name"`
	Listen   string `json:"listen"`
	Upstream string `json:"upstream"`
	Conns    int    `json:"conns"`
	Fault    Fault  `json:"fault"`
}

// Proxies describes every proxy.
func (a *Agent) Proxies() []ProxyInfo {
	out := make([]ProxyInfo, 0, len(a.proxies))
	for _, p := range a.proxies {
		out = append(out, ProxyInfo{Name: p.Name, Listen: p.Addr(), Upstream: p.Upstream, Conns: p.Conns(), Fault: p.Fault()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (a *Agent) proxyNames() string {
	var names []string
	for _, p := range a.Proxies() {
		names = append(names, p.Name)
	}
	if len(names) == 0 {
		return "none"
	}
	return fmt.Sprint(names)
}
