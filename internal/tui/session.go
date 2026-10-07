package tui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Session is one console conversation, saved so it can be picked up again
// with `stampede --continue`, `stampede --resume <id>` or /resume.
type Session struct {
	ID      string    `json:"id"`
	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
	Server  string    `json:"server,omitempty"`
	Project string    `json:"project,omitempty"`
	// Title is the first thing typed in the session.
	Title  string   `json:"title,omitempty"`
	Inputs int      `json:"inputs"`
	Lines  []string `json:"lines"`
}

// NewSession starts an empty session.
func NewSession() *Session {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	now := time.Now()
	return &Session{ID: now.Format("20060102-150405") + "-" + hex.EncodeToString(b), Started: now, Updated: now}
}

// Store keeps sessions and the input history under the user's config
// directory (~/.config/stampede/console on Linux), readable only by them.
type Store struct {
	dir string
}

const (
	keepSessions = 50
	keepHistory  = 1000
)

// OpenStore opens the default store, creating its directory.
func OpenStore() (*Store, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return OpenStoreAt(filepath.Join(dir, "stampede", "console"))
}

// OpenStoreAt opens a store in dir.
func OpenStoreAt(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, "sessions", id+".json") }

// Save writes the session and drops the oldest beyond the last 50.
func (s *Store) Save(sess *Session) error {
	sess.Updated = time.Now()
	b, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	_, statErr := os.Stat(s.path(sess.ID))
	if err := writeFileAtomic(s.path(sess.ID), b); err != nil {
		return err
	}
	if statErr == nil {
		return nil // pruned when the session was first saved
	}
	if all, err := s.List(0); err == nil && len(all) > keepSessions {
		for _, old := range all[keepSessions:] {
			_ = os.Remove(s.path(old.ID))
		}
	}
	return nil
}

// List returns saved sessions, most recently used first, without their
// transcripts; n <= 0 returns all.
func (s *Store) List(n int) ([]Session, error) {
	files, err := filepath.Glob(filepath.Join(s.dir, "sessions", "*.json"))
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, f := range files {
		sess, err := readSession(f)
		if err != nil {
			continue
		}
		sess.Lines = nil
		out = append(out, *sess)
	}
	slices.SortFunc(out, func(a, b Session) int { return b.Updated.Compare(a.Updated) })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// Load returns the session whose ID is id or starts with it; an empty id
// means the most recent one.
func (s *Store) Load(id string) (*Session, error) {
	all, err := s.List(0)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, errors.New("no saved sessions yet")
	}
	if id == "" {
		return readSession(s.path(all[0].ID))
	}
	var match []string
	for _, x := range all {
		if x.ID == id {
			return readSession(s.path(x.ID))
		}
		if strings.HasPrefix(x.ID, id) {
			match = append(match, x.ID)
		}
	}
	switch len(match) {
	case 0:
		return nil, fmt.Errorf("no session %q; /sessions lists them", id)
	case 1:
		return readSession(s.path(match[0]))
	}
	return nil, fmt.Errorf("%q matches %d sessions; give more of the id", id, len(match))
}

func readSession(path string) (*Session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(b, &sess); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &sess, nil
}

// History returns the lines typed in earlier sessions, oldest first.
func (s *Store) History() []string {
	b, err := os.ReadFile(filepath.Join(s.dir, "history"))
	if err != nil {
		return nil
	}
	var h []string
	for l := range strings.SplitSeq(strings.TrimRight(string(b), "\n"), "\n") {
		if l != "" {
			h = append(h, l)
		}
	}
	return h
}

// AddHistory appends a typed line, keeping the last 1000.
func (s *Store) AddHistory(line string) error {
	if strings.ContainsAny(line, "\r\n") {
		return nil
	}
	h := append(s.History(), line)
	if len(h) > keepHistory {
		h = h[len(h)-keepHistory:]
	}
	return writeFileAtomic(filepath.Join(s.dir, "history"), []byte(strings.Join(h, "\n")+"\n"))
}

func writeFileAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone already after the rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ago renders how long ago t was, coarsely.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	}
	return t.Local().Format("Jan 2")
}

// hiddenFlags are flags whose values are never written to the history or a
// saved session.
var hiddenFlags = []string{"token", "password", "api-key", "key", "secret", "value"}

// redact hides the values of secret-looking flags in a typed line.
func redact(line string) string {
	f := strings.Fields(line)
	for i := 0; i < len(f); i++ {
		name, val, hasEq := strings.Cut(strings.TrimLeft(f[i], "-"), "=")
		if !strings.HasPrefix(f[i], "--") || !slices.Contains(hiddenFlags, name) {
			continue
		}
		if hasEq {
			if val != "" {
				f[i] = "--" + name + "=‹hidden›"
			}
		} else if i+1 < len(f) && !strings.HasPrefix(f[i+1], "--") {
			f[i+1] = "‹hidden›"
			i++
		}
	}
	return strings.Join(f, " ")
}

func (m *Model) listSessions() {
	if m.opts.Store == nil {
		m.say("Sessions are not being saved here.")
		return
	}
	all, err := m.opts.Store.List(15)
	if err != nil {
		m.say(sBad.Render(err.Error()))
		return
	}
	if len(all) == 0 {
		m.say("No saved sessions yet; this one is saved once you type something.")
		return
	}
	for _, s := range all {
		mark := " "
		if m.sess != nil && s.ID == m.sess.ID {
			mark = "*"
		}
		m.say(fmt.Sprintf(" %s %s  %-12s %3d inputs  %s", mark, s.ID, ago(s.Updated), s.Inputs, s.Title))
	}
	m.say(sMuted.Render("/resume <id> (or its start) to go back to one; * is this session."))
}

func (m *Model) resume(c Command) {
	if m.opts.Store == nil {
		m.say("Sessions are not being saved here.")
		return
	}
	// A session whose only input is this /resume is not worth keeping.
	if m.sess != nil && m.sess.Inputs > 1 {
		m.save()
	}
	id := ""
	if len(c.Args) > 0 {
		id = c.Args[0]
	}
	var s *Session
	var err error
	if id == "" {
		// The most recent session other than this one.
		var all []Session
		if all, err = m.opts.Store.List(0); err == nil {
			err = errors.New("no earlier session to resume")
			for _, x := range all {
				if m.sess == nil || x.ID != m.sess.ID {
					s, err = m.opts.Store.Load(x.ID)
					break
				}
			}
		}
	} else {
		s, err = m.opts.Store.Load(id)
	}
	if err != nil {
		m.say(sBad.Render(err.Error()))
		return
	}
	if m.sess != nil && s.ID == m.sess.ID {
		m.say("That is this session.")
		return
	}
	m.resumeSession(s)
}
