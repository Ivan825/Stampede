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

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestSessionSavedAndResumed(t *testing.T) {
	st, err := OpenStoreAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New(nil, Options{Store: st})
	if !strings.Contains(strings.Join(m.lines, "\n"), "/exit to leave") {
		t.Error("welcome does not say how to leave")
	}
	typeLine(m, "/use shop")
	typeLine(m, "/help")
	id := m.sess.ID

	all, err := st.List(0)
	if err != nil || len(all) != 1 || all[0].ID != id || all[0].Inputs != 2 || all[0].Title != "/use shop" {
		t.Fatalf("saved sessions %+v, %v", all, err)
	}
	if got := st.History(); !slices.Equal(got, []string{"/use shop", "/help"}) {
		t.Errorf("history %q", got)
	}

	// A new console offers the last session and remembers the history.
	m2 := New(nil, Options{Store: st})
	if !strings.Contains(strings.Join(m2.lines, "\n"), "/resume picks it up") {
		t.Error("the last session is not offered")
	}
	if len(m2.history) != 2 {
		t.Errorf("history not loaded: %q", m2.history)
	}
	typeLine(m2, "/resume")
	if m2.sess.ID != id || m2.project != "shop" {
		t.Fatalf("resumed %s project %q", m2.sess.ID, m2.project)
	}
	text := strings.Join(m2.lines, "\n")
	if !strings.Contains(text, "/run <shape>") || !strings.Contains(text, "resumed the session") {
		t.Error("the transcript was not restored")
	}

	// stampede --resume <start of id>
	if all, _ := st.List(0); len(all) != 1 {
		t.Errorf("%d sessions saved; a session that only resumed another is not kept", len(all))
	}
	s, err := st.Load(id[:len(id)-3])
	if err != nil || s.ID != id {
		t.Fatalf("load by prefix: %v", err)
	}
	m3 := New(nil, Options{Store: st, Resume: s})
	if m3.sess.ID != id || m3.project != "shop" {
		t.Error("Options.Resume not applied")
	}
	if fi, err := os.Stat(st.path(id)); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) {
		t.Errorf("session file mode: %v %v", fi.Mode(), err)
	}
}

func TestEmptySessionNotSaved(t *testing.T) {
	st, _ := OpenStoreAt(t.TempDir())
	m := New(nil, Options{Store: st})
	if m.save() {
		t.Error("a session with nothing typed was saved")
	}
	if all, _ := st.List(0); len(all) != 0 {
		t.Errorf("%d sessions saved", len(all))
	}
}

func TestSecretsNotSaved(t *testing.T) {
	cases := map[string]string{
		"/login --server http://x --token abc123": "/login --server http://x --token ‹hidden›",
		"/ai providers set p --api-key=sk-1":      "/ai providers set p --api-key=‹hidden›",
		"/projects list --json":                   "/projects list --json",
	}
	for in, want := range cases {
		if got := redact(in); got != want {
			t.Errorf("redact(%q) = %q, want %q", in, got, want)
		}
	}
	st, _ := OpenStoreAt(t.TempDir())
	m := New(nil, Options{Store: st})
	typeLine(m, "/login --token abc123")
	b, _ := os.ReadFile(filepath.Join(st.dir, "history"))
	saved, _ := os.ReadFile(st.path(m.sess.ID))
	if strings.Contains(string(b)+string(saved), "abc123") {
		t.Error("a token was written to disk")
	}
}

func TestLeaving(t *testing.T) {
	for _, w := range []string{"exit", "quit", "/exit", "/quit", "bye"} {
		m := New(nil, Options{})
		m.input.SetValue(w)
		if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); !isQuit(cmd) {
			t.Errorf("%q did not quit", w)
		}
	}
	m := New(nil, Options{})
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); isQuit(cmd) {
		t.Fatal("one Ctrl-C quit")
	}
	if !strings.Contains(m.lines[len(m.lines)-1], "Ctrl-C again") {
		t.Errorf("no hint: %q", m.lines[len(m.lines)-1])
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); !isQuit(cmd) {
		t.Error("a second Ctrl-C did not quit")
	}
}
