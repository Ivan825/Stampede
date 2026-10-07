package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSplitArgs(t *testing.T) {
	cases := map[string][]string{
		`projects create shop`:                          {"projects", "create", "shop"},
		`targets create "my shop" --base-url  http://x`: {"targets", "create", "my shop", "--base-url", "http://x"},
		`notify test 'a "b" c'`:                         {"notify", "test", `a "b" c`},
		`a\ b "c\"d"`:                                   {"a b", `c"d`},
		`x ""`:                                          {"x", ""},
	}
	for in, want := range cases {
		got, err := SplitArgs(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("SplitArgs(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := SplitArgs(`x "open`); err == nil {
		t.Error("unclosed quote accepted")
	}
}

// fakeStampede writes a script standing in for the stampede binary that
// prints its arguments.
func fakeStampede(t *testing.T) string {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script")
	}
	p := filepath.Join(t.TempDir(), "stampede")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho \"ran: $*\"\n[ \"$1\" = fail ] && exit 2\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEveryCommandRunsInTheConsole(t *testing.T) {
	m := New(nil, Options{
		Self:     fakeStampede(t),
		Commands: map[string]Kind{"projects": Inline, "runs": Inline, "fail": Inline, "server": Daemon, "tokens": Terminal},
	})
	run := func(line string) string {
		for _, msg := range runCmd(m.handle(line)) {
			if done, ok := msg.(externalDoneMsg); ok {
				m.externalDone(done)
			}
		}
		return strings.Join(m.lines, "\n")
	}
	if out := run(`/projects create "my shop"`); !strings.Contains(out, "ran: projects create my shop") {
		t.Errorf("inline command output missing:\n%s", out)
	}
	// /runs alone is the console's own list; with arguments it is the CLI's.
	if out := run("/runs show abc"); !strings.Contains(out, "ran: runs show abc") {
		t.Errorf("/runs show did not go to the CLI:\n%s", out)
	}
	if out := run("/stampede projects list"); !strings.Contains(out, "ran: projects list") {
		t.Errorf("/stampede passthrough:\n%s", out)
	}
	if out := run("/fail now"); !strings.Contains(out, "exited with code 2") {
		t.Errorf("failure not reported:\n%s", out)
	}
	if out := run("/server"); !strings.Contains(out, "another terminal") {
		t.Errorf("daemon not refused:\n%s", out)
	}
	if out := run("/nonsense"); !strings.Contains(out, "Unknown command /nonsense") {
		t.Errorf("unknown command:\n%s", out)
	}
	if m.proc != nil {
		t.Error("a finished command left proc set")
	}
}

// runCmd runs cmd and any batch it returns, and collects the messages.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, runCmd(c)...)
		}
		return out
	case nil:
		return nil
	default:
		return []tea.Msg{msg}
	}
}
