package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Ivan825/Stampede/internal/scenario"
)

// do handles a line and runs the command it returns to completion, as
// Bubble Tea would, feeding the result back into the model.
func do(m *Model, line string) tea.Msg {
	var last tea.Msg
	for _, msg := range runCmd(m.handle(line)) {
		if _, tick := msg.(animMsg); tick {
			continue
		}
		m.Update(msg)
		last = msg
	}
	return last
}

func last(m *Model) string { return strings.Join(m.lines, "\n") }

// counting starts a server that answers 200 and counts requests.
func counting(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { n.Add(1) }))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestRunPositionalDuration(t *testing.T) {
	srv, hits := counting(t)
	file := filepath.Join(t.TempDir(), "home.yaml")
	yaml := fmt.Sprintf("metadata: {name: home}\ntarget: {baseURL: %q}\njourneys:\n  - name: home\n    steps:\n      - get: /\nload: {mode: rate, rate: 20/s, duration: 10m}\n", srv.URL)
	if err := os.WriteFile(file, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(nil, Options{})
	start := time.Now()
	// The 10m in the file gives way to the 1s typed after /run.
	msg := do(m, "/run 1s --file "+file)
	if _, ok := msg.(doneMsg); !ok || time.Since(start) > 30*time.Second || hits.Load() == 0 {
		t.Fatalf("run: %#v after %v, %d requests\n%s", msg, time.Since(start), hits.Load(), last(m))
	}
	if !strings.Contains(last(m), "home") {
		t.Errorf("no summary: %s", last(m))
	}

	m = New(nil, Options{})
	do(m, "/run soak 4h 5h --file "+file)
	if !strings.Contains(m.lines[len(m.lines)-1], "Two durations") || m.live != nil {
		t.Errorf("two durations: %q", m.lines[len(m.lines)-1])
	}
	do(m, "/run soak 4h extra --file "+file)
	if !strings.Contains(m.lines[len(m.lines)-1], `Unexpected "extra"`) {
		t.Errorf("extra argument: %q", m.lines[len(m.lines)-1])
	}
}

func TestRunOverrides(t *testing.T) {
	cases := []struct {
		line, shape, duration, scenario, problem string
	}{
		{"/run soak 4h", "soak", "4h", "", ""},
		{"/run soak 4h checkout-flow", "soak", "4h", "checkout-flow", ""},
		{"/run spike checkout-flow --duration 5m", "spike", "5m", "checkout-flow", ""},
		{"/run 30s", "", "30s", "", ""},
		{"/run soak 4h --duration 5m", "", "", "", "Two durations"},
		{"/run zigzag", "", "", "", "Unknown shape"},
		{"/run soak a b", "", "", "", `Unexpected "b"`},
	}
	for _, c := range cases {
		cmd, _ := ParseSlash(c.line)
		ov, problem := runOverrides(cmd)
		if c.problem != "" {
			if !strings.Contains(problem, c.problem) {
				t.Errorf("%s: problem %q, want %q", c.line, problem, c.problem)
			}
			continue
		}
		if problem != "" || ov.Shape != c.shape || ov.Duration != c.duration || cmd.Flags["scenario"] != c.scenario {
			t.Errorf("%s: %+v scenario %q problem %q", c.line, ov, cmd.Flags["scenario"], problem)
		}
	}
}

func TestRunReplay(t *testing.T) {
	srv, hits := counting(t)
	log := filepath.Join(t.TempDir(), "access.log")
	lines := `10.0.0.1 - - [07/Oct/2026:10:00:00 +0000] "GET /api/products?page=1 HTTP/1.1" 200 512 "-" "-"
10.0.0.1 - - [07/Oct/2026:10:00:01 +0000] "GET /api/products/42 HTTP/1.1" 200 128 "-" "-"
10.0.0.3 - - [07/Oct/2026:10:00:02 +0000] "GET /api/products/7 HTTP/1.1" 200 128 "-" "-"
`
	if err := os.WriteFile(log, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	var checked atomic.Bool
	m := New(nil, Options{CheckTarget: func(_ context.Context, _ io.Writer, s *scenario.Scenario, _ map[string]string) (func(*url.URL) bool, error) {
		checked.Store(s.Target.BaseURL == srv.URL)
		return func(*url.URL) bool { return true }, nil
	}})
	msg := do(m, "/run replay "+log+" --target "+srv.URL+" --speed 4")
	if _, ok := msg.(doneMsg); !ok || hits.Load() != 3 || !checked.Load() {
		t.Fatalf("replay: %#v, %d requests, checked %v\n%s", msg, hits.Load(), checked.Load(), last(m))
	}
	if !strings.Contains(last(m), "GET api.products.{id}") {
		t.Errorf("summary does not show the replayed endpoints: %s", last(m))
	}

	t.Setenv("TARGET_URL", "")
	m = New(nil, Options{})
	do(m, "/run replay "+log)
	if !strings.Contains(m.lines[len(m.lines)-1], "--target") || m.live != nil {
		t.Errorf("replay without a target: %q", m.lines[len(m.lines)-1])
	}
	do(m, "/run replay "+log+" --target "+srv.URL+" --speed fast")
	if !strings.Contains(m.lines[len(m.lines)-1], "--speed") {
		t.Errorf("bad speed: %q", m.lines[len(m.lines)-1])
	}
	msg = do(m, "/run replay missing.log --target "+srv.URL)
	if d, ok := msg.(doneMsg); !ok || !strings.Contains(d.text, "load.replay.file") {
		t.Errorf("missing recording: %#v", msg)
	}
}

func TestLocalRunSafetyHook(t *testing.T) {
	srv, hits := counting(t)
	file := filepath.Join(t.TempDir(), "home.yaml")
	yaml := fmt.Sprintf("metadata: {name: home}\ntarget: {baseURL: %q}\njourneys:\n  - name: home\n    steps:\n      - get: /\nload: {iterations: 3}\n", srv.URL)
	if err := os.WriteFile(file, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(nil, Options{CheckTarget: func(_ context.Context, w io.Writer, _ *scenario.Scenario, _ map[string]string) (func(*url.URL) bool, error) {
		fmt.Fprintln(w, "checking the target")
		return nil, errors.New("public host whose ownership is not verified")
	}})
	msg := do(m, "/run smoke --file "+file)
	if d, ok := msg.(doneMsg); !ok || !strings.Contains(d.text, "not verified") || hits.Load() != 0 {
		t.Errorf("blocked run: %#v, %d requests", msg, hits.Load())
	}
}

func TestInit(t *testing.T) {
	var got []string
	m := New(nil, Options{Init: func(_ context.Context, w io.Writer, target, dir string, env []string) error {
		got = append([]string{target, dir}, env...)
		fmt.Fprintln(w, "This looks like a shop.")
		fmt.Fprint(w, "Installed 7 files.")
		if target == "http://broken" {
			return errors.New("cannot reach http://broken")
		}
		return nil
	}})
	do(m, "/init http://localhost:8090 --dir packs --env MQTT_BROKER=tcp://localhost:1883")
	if strings.Join(got, " ") != "http://localhost:8090 packs MQTT_BROKER=tcp://localhost:1883" {
		t.Errorf("init called with %q", got)
	}
	if !strings.HasSuffix(last(m), "This looks like a shop.\nInstalled 7 files.") {
		t.Errorf("output: %s", last(m))
	}
	do(m, "/init http://broken")
	if !strings.Contains(last(m), "cannot reach http://broken") {
		t.Errorf("error: %s", last(m))
	}
	do(m, "/init")
	if !strings.Contains(m.lines[len(m.lines)-1], "usage: /init <url>") {
		t.Errorf("usage: %q", m.lines[len(m.lines)-1])
	}
	m = New(nil, Options{})
	do(m, "/init http://localhost:8090")
	if strings.Contains(last(m), "planned") || !strings.Contains(m.lines[len(m.lines)-1], "stampede init --target http://localhost:8090") {
		t.Errorf("without the hook: %q", m.lines[len(m.lines)-1])
	}
}

func TestLineWriter(t *testing.T) {
	var sent []string
	w := &lineWriter{send: func(m tea.Msg) { sent = append(sent, string(m.(logMsg))) }}
	fmt.Fprint(w, "one\ntw")
	fmt.Fprint(w, "o\nthree")
	if strings.Join(sent, "|") != "one|two" || w.rest() != "three" || w.rest() != "" {
		t.Errorf("sent %q", sent)
	}
}
