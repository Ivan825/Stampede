package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Ivan825/Stampede/gen/stampede/plugin/v1"
	"github.com/Ivan825/Stampede/internal/version"
	"github.com/Ivan825/Stampede/pkg/pluginsdk"
)

// ErrCrashed fails calls to a plugin process that has exited.
var ErrCrashed = errors.New("plugin process exited")

// restartEvery limits how often a crashed plugin is restarted.
const restartEvery = time.Second

// StepType is a step a plugin offers, with its compiled config schema.
type StepType struct {
	Name        string
	Description string
	// Schema is the step's config schema as the plugin sent it.
	Schema   []byte
	Compiled *jsonschema.Schema
	// Targets are the top-level config properties that name the host the
	// step connects to ("x-stampede-target": true).
	Targets []string
}

// Plugin is a running plugin. A crashed process is restarted on the next
// call (at most once a second); sessions opened in the old process fail
// with ErrCrashed and must be opened again.
type Plugin struct {
	Name, Version, Description string
	Path                       string
	Steps                      map[string]*StepType

	log *slog.Logger

	mu        sync.Mutex
	cur       *process
	gen       uint64
	lastStart time.Time
	killed    bool
}

type process struct {
	client *plugin.Client
	svc    pluginv1.PluginServiceClient
	gen    uint64
}

func (p *process) exited() bool { return p.client.Exited() }

// crashed reports whether a failed call failed because the process died.
// The connection usually breaks a moment before go-plugin notices the
// exit, so an unavailable connection is given a little time to show it.
func (p *process) crashed(err error) bool {
	if p.exited() {
		return true
	}
	if status.Code(err) != codes.Unavailable {
		return false
	}
	for range 50 {
		time.Sleep(10 * time.Millisecond)
		if p.exited() {
			return true
		}
	}
	return false
}

// Start launches the plugin executable at path and asks it to describe
// itself. The description is validated: a plugin with an invalid name or
// a schema that does not compile is refused.
func Start(ctx context.Context, path string, log *slog.Logger) (*Plugin, error) {
	if log == nil {
		log = slog.Default()
	}
	p := &Plugin{Path: path, log: log}
	proc, err := p.launch()
	if err != nil {
		return nil, err
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d, err := proc.svc.Describe(dctx, &pluginv1.DescribeRequest{HostVersion: version.Version})
	if err != nil {
		proc.client.Kill()
		return nil, fmt.Errorf("plugin %s: describe: %w", path, err)
	}
	if err := pluginsdk.ValidateDescription(d); err != nil {
		proc.client.Kill()
		return nil, fmt.Errorf("plugin %s describes itself incorrectly: %w", path, err)
	}
	p.Name, p.Version, p.Description = d.GetName(), d.GetVersion(), d.GetDescription()
	p.Steps = map[string]*StepType{}
	for _, st := range d.GetSteps() {
		c, _ := pluginsdk.CompileSchema(st.GetName(), st.GetConfigSchema())
		targets, _ := pluginsdk.TargetFields(st.GetConfigSchema())
		sort.Strings(targets)
		p.Steps[st.GetName()] = &StepType{
			Name: st.GetName(), Description: st.GetDescription(),
			Schema: st.GetConfigSchema(), Compiled: c, Targets: targets,
		}
	}
	p.cur = proc
	return p, nil
}

// launch starts a new process of the plugin.
func (p *Plugin) launch() (*process, error) {
	name := p.Name
	if name == "" {
		name = strings.TrimPrefix(filepath.Base(p.Path), BinaryPrefix)
	}
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  pluginsdk.Handshake,
		Plugins:          plugin.PluginSet{pluginsdk.PluginKey: &pluginsdk.GRPCPlugin{}},
		Cmd:              exec.Command(p.Path),
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		StartTimeout:     15 * time.Second,
		Logger:           hclogTo(p.log, name),
	})
	rpc, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("plugin %s: %w", name, err)
	}
	raw, err := rpc.Dispense(pluginsdk.PluginKey)
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("plugin %s: %w", name, err)
	}
	svc, ok := raw.(pluginv1.PluginServiceClient)
	if !ok {
		client.Kill()
		return nil, fmt.Errorf("plugin %s: unexpected client type %T", name, raw)
	}
	p.gen++
	p.lastStart = time.Now()
	return &process{client: client, svc: svc, gen: p.gen}, nil
}

// live returns the running process, restarting a crashed one.
func (p *Plugin) live() (*process, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.killed {
		return nil, errors.New("plugin " + p.Name + " has been stopped")
	}
	if !p.cur.exited() {
		return p.cur, nil
	}
	if time.Since(p.lastStart) < restartEvery {
		return nil, ErrCrashed
	}
	p.log.Warn("plugin exited; restarting it", "plugin", p.Name)
	proc, err := p.launch()
	if err != nil {
		return nil, err
	}
	p.cur = proc
	return proc, nil
}

// Kill stops the plugin process.
func (p *Plugin) Kill() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.killed = true
	if p.cur != nil {
		p.cur.client.Kill()
	}
}

// Describe returns the plugin's description as it sent it.
func (p *Plugin) Describe() *pluginv1.DescribeResponse {
	d := &pluginv1.DescribeResponse{Name: p.Name, Version: p.Version, Description: p.Description}
	names := make([]string, 0, len(p.Steps))
	for n := range p.Steps {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		st := p.Steps[n]
		d.Steps = append(d.Steps, &pluginv1.StepType{Name: st.Name, Description: st.Description, ConfigSchema: st.Schema})
	}
	return d
}

// StepNames lists the plugin's steps, sorted.
func (p *Plugin) StepNames() []string {
	out := make([]string, 0, len(p.Steps))
	for n := range p.Steps {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Session is one virtual user's session in a plugin process.
type Session struct {
	p    *Plugin
	proc *process
	id   string
}

// Open starts a session for a virtual user.
func (p *Plugin) Open(ctx context.Context, vu int64, runID string) (*Session, error) {
	proc, err := p.live()
	if err != nil {
		return nil, err
	}
	octx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r, err := proc.svc.Open(octx, &pluginv1.OpenRequest{Vu: vu, RunId: runID})
	if err != nil {
		if proc.crashed(err) {
			return nil, ErrCrashed
		}
		return nil, err
	}
	return &Session{p: p, proc: proc, id: r.GetSession()}, nil
}

// Execute runs one step. An error means the plugin could not run it at
// all (ErrCrashed when its process is gone); a step that ran and failed
// is a response with Ok false.
func (s *Session) Execute(ctx context.Context, req *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
	if s.proc.exited() {
		return nil, ErrCrashed
	}
	req.Session = s.id
	// The plugin bounds the step itself; the extra second lets it report
	// its own timeout before the call is cut off.
	cctx, cancel := context.WithTimeout(ctx, time.Duration(req.GetTimeoutNs())+time.Second)
	defer cancel()
	r, err := s.proc.svc.Execute(cctx, req)
	if err != nil && s.proc.crashed(err) {
		return nil, ErrCrashed
	}
	return r, err
}

// Stale reports whether the session's process has exited.
func (s *Session) Stale() bool { return s.proc.exited() }

// Close ends the session; errors are ignored, as the user is gone.
func (s *Session) Close(ctx context.Context) {
	if s.proc.exited() {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _ = s.proc.svc.Close(cctx, &pluginv1.CloseRequest{Session: s.id})
}

// Set is the plugins a run uses, by name.
type Set map[string]*Plugin

// Load finds and starts the named plugins. dir is the plugin directory
// (Dir() when empty). A plugin that is missing or fails to start fails
// the whole load, naming it.
func Load(ctx context.Context, dir string, names []string, log *slog.Logger) (Set, error) {
	set := Set{}
	for _, name := range names {
		if _, ok := set[name]; ok {
			continue
		}
		path, err := Find(dir, name)
		if err != nil {
			set.Kill()
			return nil, err
		}
		p, err := Start(ctx, path, log)
		if err != nil {
			set.Kill()
			return nil, err
		}
		if p.Name != name {
			p.Kill()
			set.Kill()
			return nil, fmt.Errorf("%s describes itself as plugin %q, not %q", path, p.Name, name)
		}
		set[name] = p
	}
	return set, nil
}

// Kill stops every plugin in the set.
func (s Set) Kill() {
	for _, p := range s {
		p.Kill()
	}
}

// hclogTo sends go-plugin's log (including what the plugin logs) to slog.
// Only warnings and errors are kept.
func hclogTo(log *slog.Logger, name string) hclog.Logger {
	return hclog.New(&hclog.LoggerOptions{
		Name:        "plugin",
		Level:       hclog.Warn,
		DisableTime: true,
		Output:      &slogWriter{log: log.With("plugin", name)},
	})
}

// slogWriter logs each hclog entry (one Write) as one slog record.
type slogWriter struct {
	log *slog.Logger
}

func (w *slogWriter) Write(b []byte) (int, error) {
	msg := strings.TrimSpace(string(b))
	switch {
	case msg == "":
	case strings.HasPrefix(msg, "[ERROR]"):
		w.log.Error(strings.TrimSpace(strings.TrimPrefix(msg, "[ERROR]")))
	default:
		w.log.Warn(strings.TrimSpace(strings.TrimPrefix(msg, "[WARN]")))
	}
	return len(b), nil
}

var _ io.Writer = (*slogWriter)(nil)
