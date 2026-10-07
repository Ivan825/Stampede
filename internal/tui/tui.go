package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/runner"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/version"
)

var (
	accent  = lipgloss.AdaptiveColor{Light: "#0D9488", Dark: "#14B8A6"}
	muted   = lipgloss.AdaptiveColor{Light: "#52525B", Dark: "#A1A1AA"}
	good    = lipgloss.AdaptiveColor{Light: "#17864F", Dark: "#3CCB7F"}
	bad     = lipgloss.AdaptiveColor{Light: "#D12D2D", Dark: "#FF5C5C"}
	sTitle  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	sMuted  = lipgloss.NewStyle().Foreground(muted)
	sGood   = lipgloss.NewStyle().Foreground(good)
	sBad    = lipgloss.NewStyle().Foreground(bad)
	sBox    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(muted).Padding(0, 1)
	sBigNum = lipgloss.NewStyle().Bold(true)
)

// bull is the block-art mark (the same grid as brand/build.py).
var bull = []string{
	"█           █",
	"██         ██",
	" ███████████",
	"  █████████",
	"  ██ ███ ██",
	"  █████████",
	"   ███████",
	"   █ ███ █",
	"   ███████",
	"   ██   ██",
}

// banner draws the bull with the name and version beside it.
func banner() string {
	art := lipgloss.NewStyle().Foreground(accent).Render(strings.Join(bull, "\n"))
	text := sTitle.Render("STAMPEDE") + "\n" + sMuted.Render(version.Version) + "\n\n" +
		sMuted.Render("Describe your users. Stampede becomes a thousand of them.")
	return lipgloss.JoinHorizontal(lipgloss.Center, art, "   ", text) + "\n"
}

// Options connect the console to commands that live in the CLI.
type Options struct {
	// Init is stampede init with the confirmation already given: it
	// detects the target's product type, installs the matching pack into
	// dir and dry-runs it, writing its progress to w.
	Init func(ctx context.Context, w io.Writer, target, dir string, env []string) error
	// CheckTarget applies stampede run's safety rules to a local run of
	// s and returns the host policy its requests must pass. Messages go
	// to w. Nil leaves local runs without a host policy.
	CheckTarget func(ctx context.Context, w io.Writer, s *scenario.Scenario, env map[string]string) (func(*url.URL) bool, error)
}

// Model is the Bubble Tea model.
type Model struct {
	c       *client.Client
	opts    Options
	send    func(tea.Msg)
	input   textinput.Model
	view    viewport.Model
	lines   []string
	width   int
	height  int
	project string
	header  string

	scenarios []string
	pending   *Command

	live    *liveRun
	history []string
	histPos int
}

type liveRun struct {
	id       string
	name     string
	cancel   context.CancelFunc
	points   []gen.Point
	status   string
	planned  time.Duration
	started  time.Time
	localRun bool
}

type (
	logMsg     string
	headerMsg  string
	pointMsg   gen.Point
	statusMsg  string
	doneMsg    struct{ text string }
	refreshMsg struct {
		header    string
		scenarios []string
	}
)

// New builds the model. c may be nil, in which case /run works on local
// scenario files with the in-process engine.
func New(c *client.Client, opts Options) *Model {
	in := textinput.New()
	in.Placeholder = "type /help, a slash command, or what you want to test"
	in.Prompt = "› "
	in.Focus()
	in.CharLimit = 500
	m := &Model{c: c, opts: opts, input: in, view: viewport.New(80, 20)}
	m.say(banner())
	if c == nil {
		m.say("Not signed in to a server, so runs use local files with the built-in engine:")
		m.say("  /run smoke --file checkout.yaml      or sign in with: stampede login")
	} else {
		m.say("Connected to " + c.Server + ". Type /help for commands.")
	}
	return m
}

// Run starts the program on the terminal.
func Run(c *client.Client, opts Options) error {
	m := New(c, opts)
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.send = p.Send
	_, err := p.Run()
	return err
}

func (m *Model) say(s string) {
	m.lines = append(m.lines, strings.Split(s, "\n")...)
	if len(m.lines) > 2000 {
		m.lines = m.lines[len(m.lines)-2000:]
	}
	m.view.SetContent(strings.Join(m.lines, "\n"))
	m.view.GotoBottom()
}

// Init loads the header and scenario names.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.refresh())
}

func (m *Model) refresh() tea.Cmd {
	if m.c == nil {
		return nil
	}
	c, project := m.c, m.project
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var ws []gen.Worker
		_ = c.Do(ctx, "GET", "/workers", nil, &ws)
		p, err := c.FindProject(ctx, project)
		if err != nil {
			return headerMsg(fmt.Sprintf("%s · %d workers · %s", c.Server, len(ws), err))
		}
		var ss []gen.Scenario
		_ = c.Do(ctx, "GET", "/projects/"+p.Id.String()+"/scenarios", nil, &ss)
		names := make([]string, 0, len(ss))
		for _, s := range ss {
			names = append(names, s.Name)
		}
		return refreshMsg{header: fmt.Sprintf("%s · project %s · %d workers", c.Server, p.Name, len(ws)), scenarios: names}
	}
}

// Update handles input and background messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = msg.Width - 4
		m.layout()
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			if m.live != nil {
				m.say(sMuted.Render("Stopped following. The run continues; /stop or /kill to end it."))
				m.live.cancel()
				m.live = nil
				m.layout()
				return m, nil
			}
			return m, tea.Quit
		case tea.KeyEnter:
			line := strings.TrimSpace(m.input.Value())
			m.input.SetValue("")
			if line != "" {
				m.history = append(m.history, line)
				m.histPos = len(m.history)
				cmds = append(cmds, m.handle(line))
			}
		case tea.KeyUp:
			if m.histPos > 0 {
				m.histPos--
				m.input.SetValue(m.history[m.histPos])
			}
		case tea.KeyDown:
			if m.histPos < len(m.history)-1 {
				m.histPos++
				m.input.SetValue(m.history[m.histPos])
			} else {
				m.histPos = len(m.history)
				m.input.SetValue("")
			}
		case tea.KeyPgUp, tea.KeyPgDown:
			var c tea.Cmd
			m.view, c = m.view.Update(msg)
			cmds = append(cmds, c)
		}
	case logMsg:
		m.say(string(msg))
	case headerMsg:
		m.header = string(msg)
	case refreshMsg:
		m.header, m.scenarios = msg.header, msg.scenarios
	case liveIDMsg:
		if m.live != nil {
			m.live.id, m.live.name, m.live.planned = msg.id, msg.name, msg.planned
		}
	case pointMsg:
		if m.live != nil {
			m.live.points = append(m.live.points, gen.Point(msg))
			if len(m.live.points) > 600 {
				m.live.points = m.live.points[len(m.live.points)-600:]
			}
		}
	case statusMsg:
		if m.live != nil {
			m.live.status = string(msg)
		}
	case doneMsg:
		m.live = nil
		m.say(msg.text)
		m.layout()
		cmds = append(cmds, m.refresh())
	}
	var c tea.Cmd
	m.input, c = m.input.Update(msg)
	cmds = append(cmds, c)
	return m, tea.Batch(cmds...)
}

func (m *Model) layout() {
	liveH := 0
	if m.live != nil {
		liveH = 9
	}
	m.view.Width = m.width
	m.view.Height = max(m.height-4-liveH, 3)
}

// handle runs one line of input.
func (m *Model) handle(line string) tea.Cmd {
	m.say(sMuted.Render("› ") + line)
	if m.pending != nil {
		c := *m.pending
		m.pending = nil
		if l := strings.ToLower(line); l == "y" || l == "yes" || l == "" {
			return m.exec(c)
		}
		m.say("Cancelled.")
		if l := strings.ToLower(line); l == "n" || l == "no" {
			return nil
		}
	}
	if c, ok := ParseSlash(line); ok {
		return m.exec(c)
	}
	if c, ok := Interpret(line, m.scenarios); ok {
		m.pending = &c
		m.say(fmt.Sprintf("I would run: %s\nRun it? [Y/n]", sTitle.Render(c.String())))
		return nil
	}
	m.say("I did not understand that. Try /help, or say something like \"find the breaking point for checkout\".")
	return nil
}

const help = `Commands
  /run <shape> [duration] [scenario] [--scenario name] [--target name] [--rate 100/s]
               [--vus 50] [--duration 5m] [--start ..] [--max ..]    start a run and watch it
       shapes: smoke baseline stress spike soak breakpoint steps recovery wave
       for example: /run soak 4h checkout-flow
       without a server: /run <shape> --file scenario.yaml
  /run replay <access.log | recording.har> --target http://host:port [--speed 2]
                            replay recorded traffic with the built-in engine
  /init <url> [--dir stampede] [--env KEY=VALUE]
                            detect the product type, install the matching pack, dry-run it
  /runs                     recent runs
  /scenarios                scenarios in the project
  /workers                  connected workers
  /stop [run]  /kill [run]  stop gracefully / immediately (no run: the one you are watching)
  /kill all                 kill switch for every active run
  /use <project>            switch project
  /clear  /quit
Plain language works too, for example "spike test checkout at 300 rps for 5 minutes";
it is turned into a command and shown for confirmation first.`

func (m *Model) exec(c Command) tea.Cmd {
	switch c.Name {
	case "help", "h", "?":
		m.say(help)
	case "quit", "q", "exit":
		return tea.Quit
	case "clear":
		m.lines = nil
		m.say("")
	case "use":
		if len(c.Args) == 1 {
			m.project = c.Args[0]
			return m.refresh()
		}
		m.say("usage: /use <project>")
	case "init":
		return m.initPack(c)
	case "run":
		return m.run(c)
	case "runs":
		return m.remote(m.listRuns)
	case "scenarios":
		m.say("Scenarios: " + strings.Join(m.scenarios, ", "))
	case "workers":
		return m.remote(m.listWorkers)
	case "stop", "kill":
		return m.remote(func(ctx context.Context) string { return m.stop(ctx, c) })
	default:
		m.say("Unknown command /" + c.Name + "; try /help.")
	}
	return nil
}

func (m *Model) remote(fn func(ctx context.Context) string) tea.Cmd {
	if m.c == nil {
		m.say("This needs a server: run `stampede login` first.")
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return logMsg(fn(ctx))
	}
}

func (m *Model) listRuns(ctx context.Context) string {
	p, err := m.c.FindProject(ctx, m.project)
	if err != nil {
		return sBad.Render(err.Error())
	}
	var runs []gen.Run
	if err := m.c.Do(ctx, "GET", "/projects/"+p.Id.String()+"/runs?limit=15", nil, &runs); err != nil {
		return sBad.Render(err.Error())
	}
	var b strings.Builder
	for _, r := range runs {
		v := "-"
		if r.Verdict != nil {
			v = string(*r.Verdict)
		}
		name := ""
		if r.ScenarioName != nil {
			name = *r.ScenarioName
		}
		p95 := "-"
		if r.Summary != nil && r.Summary.P95 != nil {
			p95 = report.Ms(*r.Summary.P95)
		}
		fmt.Fprintf(&b, "  %s  %-24s %-10s %-18s p95 %-9s %s\n", r.Id.String()[:8], name, r.Status, colorVerdict(v), p95, r.CreatedAt.Local().Format("Jan 2 15:04"))
	}
	if b.Len() == 0 {
		return "No runs yet."
	}
	return strings.TrimRight(b.String(), "\n")
}

func colorVerdict(v string) string {
	switch v {
	case report.VerdictPass:
		return sGood.Render(v)
	case report.VerdictFail:
		return sBad.Render(v)
	default:
		return v
	}
}

func (m *Model) listWorkers(ctx context.Context) string {
	var ws []gen.Worker
	if err := m.c.Do(ctx, "GET", "/workers", nil, &ws); err != nil {
		return sBad.Render(err.Error())
	}
	if len(ws) == 0 {
		return "No workers connected; runs execute inside the server."
	}
	var b strings.Builder
	for _, w := range ws {
		fmt.Fprintf(&b, "  %-16s %-10s %-10s last seen %s ago\n", w.Name, w.Region, w.Status, time.Since(w.LastSeenAt).Round(time.Second))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) stop(ctx context.Context, c Command) string {
	if len(c.Args) == 1 && c.Args[0] == "all" {
		var res struct{ Killed []string }
		if err := m.c.Do(ctx, "POST", "/runs/kill-all", nil, &res); err != nil {
			return sBad.Render(err.Error())
		}
		return sBad.Render(fmt.Sprintf("Kill switch: %d run(s) killed.", len(res.Killed)))
	}
	id := ""
	if len(c.Args) == 1 {
		id = c.Args[0]
	} else if m.live != nil {
		id = m.live.id
	}
	if id == "" {
		return "Which run? /" + c.Name + " <run id>"
	}
	if len(id) < 36 {
		var err error
		if id, err = m.expandRun(ctx, id); err != nil {
			return sBad.Render(err.Error())
		}
	}
	if err := m.c.Do(ctx, "POST", "/runs/"+id+"/"+c.Name, nil, nil); err != nil {
		return sBad.Render(err.Error())
	}
	return c.Name + " requested for " + id[:8]
}

func (m *Model) expandRun(ctx context.Context, prefix string) (string, error) {
	p, err := m.c.FindProject(ctx, m.project)
	if err != nil {
		return "", err
	}
	var runs []gen.Run
	if err := m.c.Do(ctx, "GET", "/projects/"+p.Id.String()+"/runs?limit=200", nil, &runs); err != nil {
		return "", err
	}
	for _, r := range runs {
		if strings.HasPrefix(r.Id.String(), prefix) {
			return r.Id.String(), nil
		}
	}
	return "", fmt.Errorf("no run starts with %s", prefix)
}

// run starts a remote run (or a local one without a server) and follows it.
func (m *Model) run(c Command) tea.Cmd {
	if m.live != nil {
		m.say("A run is already being watched; Ctrl-C to stop following it first.")
		return nil
	}
	if len(c.Args) > 0 && c.Args[0] == "replay" {
		return m.replay(c.Args[1:], c.Flags)
	}
	ov, problem := runOverrides(c)
	if problem != "" {
		m.say(problem)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.live = &liveRun{cancel: cancel, started: time.Now(), status: "starting"}
	m.layout()
	if file := c.Flags["file"]; file != "" || m.c == nil {
		if file == "" {
			m.live = nil
			cancel()
			m.say("Without a server, give a file: " + strings.Join(append([]string{"/run"}, c.Args...), " ") + " --file scenario.yaml")
			return nil
		}
		m.live.localRun, m.live.name = true, file
		return m.runLocal(ctx, file, ov)
	}
	m.live.name = c.Flags["scenario"]
	return m.runRemote(ctx, c, ov)
}

// runOverrides reads /run's arguments: an optional shape, then an
// optional duration (/run soak 4h) and scenario name, then flags. It
// fills in c.Flags["scenario"] from a positional name. A non-empty
// problem explains what is wrong.
func runOverrides(c Command) (scenario.Overrides, string) {
	args := c.Args
	shape := ""
	if len(args) > 0 {
		if _, err := scenario.ParseDuration(args[0]); err != nil {
			shape, args = args[0], args[1:]
		}
	}
	if shape != "" && !contains(scenario.Shapes, shape) {
		return scenario.Overrides{}, fmt.Sprintf("Unknown shape %q; use one of %s or replay.", shape, strings.Join(scenario.Shapes, ", "))
	}
	ov := scenario.Overrides{Shape: shape, Rate: c.Flags["rate"], Duration: c.Flags["duration"], Start: c.Flags["start"], Max: c.Flags["max"]}
	if v := c.Flags["vus"]; v != "" {
		_, _ = fmt.Sscanf(v, "%d", &ov.VUs)
	}
	for _, a := range args {
		if _, err := scenario.ParseDuration(a); err == nil {
			if ov.Duration != "" {
				return scenario.Overrides{}, fmt.Sprintf("Two durations: %s and %s.", ov.Duration, a)
			}
			ov.Duration = a
			continue
		}
		if c.Flags["scenario"] == "" && c.Flags["file"] == "" {
			c.Flags["scenario"] = a
			continue
		}
		return scenario.Overrides{}, fmt.Sprintf("Unexpected %q; try /help.", a)
	}
	return ov, ""
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (m *Model) runRemote(ctx context.Context, c Command, ov scenario.Overrides) tea.Cmd {
	cl, project, send := m.c, m.project, m.send
	return func() tea.Msg {
		p, err := cl.FindProject(ctx, project)
		if err != nil {
			return doneMsg{sBad.Render(err.Error())}
		}
		pid := p.Id.String()
		sc, err := cl.FindScenario(ctx, pid, c.Flags["scenario"])
		if err != nil {
			return doneMsg{sBad.Render(err.Error() + " (name it with --scenario)")}
		}
		tg, err := cl.FindTarget(ctx, pid, c.Flags["target"])
		if err != nil {
			return doneMsg{sBad.Render(err.Error())}
		}
		overrides := map[string]any{}
		for k, v := range map[string]string{"shape": ov.Shape, "rate": ov.Rate, "duration": ov.Duration, "start": ov.Start, "max": ov.Max} {
			if v != "" {
				overrides[k] = v
			}
		}
		if ov.VUs > 0 {
			overrides["vus"] = ov.VUs
		}
		var run gen.Run
		if err := cl.Do(ctx, "POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc.Id, "targetId": tg.Id, "overrides": overrides}, &run); err != nil {
			return doneMsg{sBad.Render(err.Error())}
		}
		if send != nil {
			send(logMsg(fmt.Sprintf("Started %s on %s as run %s.", sc.Name, tg.BaseURL, run.Id.String()[:8])))
			send(liveIDMsg{id: run.Id.String(), name: sc.Name, planned: planned(run)})
		}
		err = cl.Follow(ctx, run.Id.String(), func(ev client.Event) {
			if send == nil {
				return
			}
			switch ev.Type {
			case "point":
				var pt gen.Point
				if json.Unmarshal(ev.Data, &pt) == nil {
					send(pointMsg(pt))
				}
			case "status":
				var r gen.Run
				if json.Unmarshal(ev.Data, &r) == nil {
					send(statusMsg(r.Status))
				}
			case "event":
				var e struct{ Message string }
				if json.Unmarshal(ev.Data, &e) == nil {
					send(logMsg(sMuted.Render("· " + e.Message)))
				}
			}
		})
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return doneMsg{sBad.Render(err.Error())}
		}
		var raw []byte
		if err := cl.Do(context.Background(), "GET", "/runs/"+run.Id.String()+"/report", nil, &raw); err != nil {
			return doneMsg{sBad.Render(err.Error())}
		}
		rep, err := report.ReadJSON(strings.NewReader(string(raw)))
		if err != nil {
			return doneMsg{sBad.Render(err.Error())}
		}
		var b strings.Builder
		rep.WriteText(&b)
		return doneMsg{strings.TrimRight(b.String(), "\n")}
	}
}

type liveIDMsg struct {
	id, name string
	planned  time.Duration
}

func planned(r gen.Run) time.Duration {
	if r.Plan == nil {
		return 0
	}
	return time.Duration(r.Plan.DurationSeconds * float64(time.Second))
}

func (m *Model) runLocal(ctx context.Context, file string, ov scenario.Overrides) tea.Cmd {
	return func() tea.Msg {
		s, err := scenario.LoadFile(file)
		if err == nil {
			err = s.LoadReplay()
		}
		if err != nil {
			return doneMsg{sBad.Render(err.Error())}
		}
		if err := ov.Apply(s); err != nil {
			return doneMsg{sBad.Render(err.Error())}
		}
		if err := s.Validate(); err != nil {
			return doneMsg{sBad.Render(err.Error())}
		}
		return m.execLocal(ctx, s)
	}
}

// replay replays an access log or HAR recording against a target with
// the in-process engine, as load.mode: replay does.
func (m *Model) replay(args []string, flags map[string]string) tea.Cmd {
	if len(args) != 1 {
		m.say("usage: /run replay <access.log | recording.har> --target http://host:port [--speed 2]")
		return nil
	}
	base := flags["target"]
	if base == "" {
		base = os.Getenv("TARGET_URL")
	}
	if base == "" {
		m.say("Give the address to replay against: /run replay " + args[0] + " --target http://localhost:8080")
		return nil
	}
	speed := 1.0
	if v := flags["speed"]; v != "" {
		if _, err := fmt.Sscanf(v, "%g", &speed); err != nil || speed <= 0 {
			m.say(fmt.Sprintf("--speed %q: give a positive number, such as 2 to replay twice as fast.", v))
			return nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.live = &liveRun{cancel: cancel, started: time.Now(), status: "loading", localRun: true, name: args[0]}
	m.layout()
	file := args[0]
	return func() tea.Msg {
		s, err := ReplayScenario(file, base, speed)
		if err != nil {
			return doneMsg{sBad.Render(err.Error())}
		}
		return m.execLocal(ctx, s)
	}
}

// ReplayScenario builds a scenario that replays a recording against base.
func ReplayScenario(file, base string, speed float64) (*scenario.Scenario, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	doc, err := json.Marshal(map[string]any{
		"metadata": map[string]any{"name": "replay"},
		"target":   map[string]any{"baseURL": base},
		"load":     map[string]any{"mode": scenario.ModeReplay, "replay": map[string]any{"file": abs, "speed": speed}},
	})
	if err != nil {
		return nil, err
	}
	s, err := scenario.Decode(doc)
	if err != nil {
		return nil, err
	}
	if err := s.LoadReplay(); err != nil {
		return nil, err
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// execLocal runs a loaded scenario with the in-process engine, sending
// live points, and returns the summary when it ends.
func (m *Model) execLocal(ctx context.Context, s *scenario.Scenario) tea.Msg {
	send := m.send
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	plan, err := s.Load.Plan()
	if err != nil {
		return doneMsg{sBad.Render(err.Error())}
	}
	var allow func(*url.URL) bool
	if m.opts.CheckTarget != nil {
		w := &lineWriter{send: send}
		allow, err = m.opts.CheckTarget(ctx, w, s, env)
		if rest := w.rest(); rest != "" && send != nil {
			send(logMsg(rest))
		}
		if err != nil {
			return doneMsg{sBad.Render(err.Error())}
		}
	}
	if send != nil {
		send(liveIDMsg{id: "local", name: s.Metadata.Name, planned: plan.TotalDuration()})
		send(statusMsg("running"))
	}
	rep, err := runner.Run(ctx, runner.Options{
		Scenario: s, Env: env, Secrets: env, AllowHost: allow,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Progress: func(p runner.Progress) {
			if send == nil {
				return
			}
			t := p.Snapshot.Totals()
			pt := gen.Point{T: p.Elapsed.Seconds() - 1, Rps: float64(t.Requests), Vus: p.Snapshot.VUs, Planned: p.Planned, Dropped: int(p.Snapshot.Dropped)} //nolint:gosec // counts fit
			if t.Requests > 0 {
				pt.ErrorRate = float64(t.Failed) / float64(t.Requests)
				pt.P95 = t.Latency.QuantileSeconds(0.95)
			}
			send(pointMsg(pt))
		},
	})
	if err != nil {
		return doneMsg{sBad.Render(err.Error())}
	}
	var b strings.Builder
	rep.WriteText(&b)
	return doneMsg{strings.TrimRight(b.String(), "\n")}
}

// initPack runs stampede init from the console.
func (m *Model) initPack(c Command) tea.Cmd {
	if len(c.Args) != 1 {
		m.say("usage: /init <url> [--dir stampede] [--env KEY=VALUE]")
		return nil
	}
	if m.opts.Init == nil {
		m.say("/init is not available here; run stampede init --target " + c.Args[0])
		return nil
	}
	target, dir := c.Args[0], c.Flags["dir"]
	var env []string
	if v := c.Flags["env"]; v != "" {
		env = []string{v}
	}
	run, send := m.opts.Init, m.send
	m.say("Looking at " + target + "...")
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		w := &lineWriter{send: send}
		err := run(ctx, w, target, dir, env)
		out := w.rest()
		if err != nil {
			out = strings.TrimLeft(out+"\n"+sBad.Render(err.Error()), "\n")
		}
		if out == "" {
			return nil
		}
		return logMsg(out)
	}
}

// lineWriter sends each complete line written to it to the console as it
// arrives. Without a send function it keeps everything for rest.
type lineWriter struct {
	send func(tea.Msg)
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if w.send == nil {
		return len(p), nil
	}
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.send(logMsg(string(w.buf[:i])))
		w.buf = w.buf[i+1:]
	}
}

// rest returns what has not been sent, without a trailing newline.
func (w *lineWriter) rest() string {
	s := strings.TrimRight(string(w.buf), "\n")
	w.buf = nil
	return s
}

// View renders the screen.
func (m *Model) View() string {
	var b strings.Builder
	head := sTitle.Render("stampede") + "  " + sMuted.Render(m.header)
	b.WriteString(head + "\n")
	b.WriteString(m.view.View() + "\n")
	if m.live != nil {
		b.WriteString(m.liveView() + "\n")
	}
	b.WriteString(m.input.View() + "\n")
	b.WriteString(sMuted.Render("enter run · ↑↓ history · pgup/pgdn scroll · ctrl-c stop following / quit"))
	return b.String()
}

func (m *Model) liveView() string {
	l := m.live
	var last gen.Point
	if n := len(l.points); n > 0 {
		last = l.points[n-1]
	}
	elapsed := time.Since(l.started).Round(time.Second)
	title := fmt.Sprintf("%s  %s  %s", sTitle.Render(l.name), l.status, sMuted.Render(elapsed.String()))
	if l.planned > 0 {
		title += sMuted.Render(" / " + l.planned.Round(time.Second).String())
	}
	num := func(label, v string) string { return sBigNum.Render(v) + " " + sMuted.Render(label) }
	errStr := report.Pct(last.ErrorRate)
	if last.ErrorRate > 0 {
		errStr = sBad.Render(errStr)
	}
	stats := strings.Join([]string{
		num("rps", fmt.Sprintf("%.0f", last.Rps)), num("p95", report.Ms(last.P95)), num("errors", errStr),
		num("vus", fmt.Sprint(last.Vus)), num("dropped", fmt.Sprint(last.Dropped)),
	}, "   ")
	w := max(m.width-24, 10)
	rps := make([]float64, 0, len(l.points))
	p95 := make([]float64, 0, len(l.points))
	for _, p := range l.points {
		rps = append(rps, p.Rps)
		p95 = append(p95, p.P95)
	}
	body := title + "\n" + stats + "\n" +
		sMuted.Render("throughput ") + lipgloss.NewStyle().Foreground(accent).Render(Sparkline(rps, w)) + "\n" +
		sMuted.Render("p95        ") + Sparkline(p95, w) + "\n" +
		sMuted.Render("/stop or /kill to end the run · ctrl-c to stop watching")
	return sBox.Width(max(m.width-2, 20)).Render(body)
}

// Sparkline draws the last width values as block characters.
func Sparkline(v []float64, width int) string {
	if len(v) > width {
		v = v[len(v)-width:]
	}
	if len(v) == 0 {
		return ""
	}
	blocks := []rune("▁▂▃▄▅▆▇█")
	hi := 0.0
	for _, x := range v {
		hi = max(hi, x)
	}
	var b strings.Builder
	for _, x := range v {
		i := 0
		if hi > 0 {
			i = int(x / hi * float64(len(blocks)-1))
		}
		b.WriteRune(blocks[i])
	}
	return b.String()
}
