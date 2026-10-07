package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Kind says how the console runs a stampede command it has no built-in
// form of.
type Kind int

const (
	// Inline runs the command with its output in the console.
	Inline Kind = iota
	// Terminal hands it the terminal: it may ask for a password, or print
	// something secret (a new token) that must not be kept in the session.
	Terminal
	// Daemon commands run until stopped and belong in their own terminal.
	Daemon
)

// ExecArg is the hidden subcommand that runs a command on the console's
// behalf with the terminal and waits for Enter before returning to it.
const ExecArg = "console-exec"

type externalDoneMsg struct {
	argv     []string
	err      error
	rest     string
	terminal bool
}

// external runs `stampede <argv...>`: inline, streaming its output into the
// console, or with the terminal for commands that prompt or print secrets.
func (m *Model) external(argv []string, kind Kind) tea.Cmd {
	switch kind {
	case Daemon:
		m.say(fmt.Sprintf("`stampede %s` runs until it is stopped; start it in another terminal.", argv[0]))
		return nil
	case Terminal:
		m.say(sMuted.Render("Handing the terminal to `stampede " + strings.Join(argv, " ") + "`…"))
		cmd := exec.Command(m.opts.Self, append([]string{ExecArg, "--"}, argv...)...) //nolint:gosec // this stampede binary with what the user typed
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			return externalDoneMsg{argv: argv, err: err, terminal: true}
		})
	}
	if !m.busy() {
		m.busySince = time.Now()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.proc = cancel
	w := &syncWriter{w: &lineWriter{send: m.send}}
	self := m.opts.Self
	return tea.Batch(m.startAnim(), func() tea.Msg {
		defer cancel()
		cmd := exec.CommandContext(ctx, self, argv...) //nolint:gosec // this stampede binary with what the user typed
		cmd.Stdout, cmd.Stderr = w, w
		err := cmd.Run()
		if ctx.Err() != nil {
			err = errors.New("stopped")
		}
		return externalDoneMsg{argv: argv, err: err, rest: w.rest()}
	})
}

func (m *Model) externalDone(msg externalDoneMsg) {
	m.proc = nil
	if msg.rest != "" {
		m.say(msg.rest)
	}
	var exit *exec.ExitError
	switch {
	case msg.err == nil && msg.terminal:
		m.say(sMuted.Render("Back in the console."))
	case msg.err == nil:
	case errors.As(msg.err, &exit):
		m.say(sBad.Render(fmt.Sprintf("stampede %s exited with code %d", msg.argv[0], exit.ExitCode())))
	default:
		m.say(sBad.Render(msg.err.Error()))
	}
}

type syncWriter struct {
	mu sync.Mutex
	w  *lineWriter
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func (s *syncWriter) rest() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.rest()
}

var _ io.Writer = (*syncWriter)(nil)

// SplitArgs splits a command line like a POSIX shell does for words:
// whitespace separates, single quotes keep everything literally, double
// quotes keep spaces, and a backslash escapes the next character.
func SplitArgs(s string) ([]string, error) {
	var (
		out   []string
		cur   strings.Builder
		inTok bool
		quote rune
	)
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(rs) && strings.ContainsRune(`"\$`+"`", rs[i+1]):
				i++
				cur.WriteRune(rs[i])
			default:
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inTok = r, true
		case r == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			inTok = true
		case r == ' ' || r == '\t':
			if inTok {
				out = append(out, cur.String())
				cur.Reset()
				inTok = false
			}
		default:
			cur.WriteRune(r)
			inTok = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unclosed quote")
	}
	if inTok {
		out = append(out, cur.String())
	}
	return out, nil
}
