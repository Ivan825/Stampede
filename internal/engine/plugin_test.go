package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/pluginhost/plugintest"
	"github.com/Ivan825/Stampede/internal/scenario"
)

func TestMain(m *testing.M) {
	code := m.Run()
	plugintest.Remove()
	os.Exit(code)
}

func stepNamed(t *testing.T, out runOut, name string) *metrics.StepStats {
	t.Helper()
	for _, st := range out.prog.Steps {
		if st.Name == name {
			if s := out.total.Steps[st.ID]; s != nil {
				return s
			}
			return &metrics.StepStats{}
		}
	}
	t.Fatalf("no step %q", name)
	return nil
}

func TestPluginSteps(t *testing.T) {
	dir := plugintest.EchoDir(t)
	var hits atomic.Int64
	var paths pathLog
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		paths.add(r.URL.Path)
	}))
	defer srv.Close()

	out := run(t, fmt.Sprintf(`
metadata: {name: plugins}
target: {baseURL: %q}
journeys:
  - name: j
    steps:
      - name: connect
        plugin: echo.connect
        extract: {reused: "$.reused"}
      - name: say
        plugin: echo.say
        with: {text: "hi-${vu}", count: "${iter}", addr: "127.0.0.1:9", tags: ["r-${reused}"]}
        check: {json: {"$.text": exists, "$.tags[0]": exists}, maxLatency: 1s}
        extract: {said: "$.text"}
      - get: /echo/${said}
      - name: refuse
        plugin: echo.say
        with: {text: x, fail: "echo   refused"}
load: {vus: 3, iterations: 9}`, srv.URL), func(o *Options) {
		o.PluginDir = dir
		o.AllowHost = func(*url.URL) bool { return true }
	})

	connect := stepNamed(t, out, "connect")
	if connect.Requests < 1 || connect.Requests > 3 || connect.Failed != 0 {
		t.Errorf("connect is recorded once per user, not when it is skipped: %d requests, %d failed", connect.Requests, connect.Failed)
	}
	if connect.PhaseSum[metrics.PhaseConnect] == 0 {
		t.Error("connect phase not recorded")
	}
	say := stepNamed(t, out, "say")
	if say.Requests != 9 || say.Failed != 0 || say.Protocols["echo"] != 9 {
		t.Errorf("say: %d requests, %d failed (%v), protocols %v", say.Requests, say.Failed, say.Errors, say.Protocols)
	}
	if p50 := time.Duration(say.Service.Quantile(0.5)) * time.Microsecond; p50 < 2*time.Millisecond || p50 > 5*time.Millisecond {
		t.Errorf("say service p50 %v, want the plugin's own 3ms", p50)
	}
	if say.PhaseSum[metrics.PhaseWait] == 0 || say.BytesOut == 0 || say.ChecksPassed != 27 {
		t.Errorf("say phases %v bytes %d checks %d", say.PhaseSum, say.BytesOut, say.ChecksPassed)
	}
	if hits.Load() != 9 || !paths.allHavePrefix("/echo/hi-") {
		t.Errorf("extracted values did not reach the next step: %d hits, paths %v", hits.Load(), paths.list())
	}
	refuse := stepNamed(t, out, "refuse")
	if refuse.Failed != 9 || refuse.Errors["echo refused"] != 9 {
		t.Errorf("refuse: %d failed, errors %v", refuse.Failed, refuse.Errors)
	}
}

func TestPluginTargetPolicy(t *testing.T) {
	dir := plugintest.EchoDir(t)
	out := run(t, `
metadata: {name: policy}
journeys:
  - name: j
    steps:
      - name: say
        plugin: echo.say
        with: {text: x, addr: "${env.ADDR}"}
load: {vus: 1, iterations: 2}`, func(o *Options) {
		o.PluginDir = dir
		o.Env = map[string]string{"ADDR": "mqtt://10.9.9.9:1883"}
		o.AllowHost = func(u *url.URL) bool { return u.Hostname() != "10.9.9.9" }
	})
	say := stepNamed(t, out, "say")
	if say.Errors["blocked by safety"] != 2 {
		t.Errorf("errors %v", say.Errors)
	}
}

func TestPluginCrashIsContained(t *testing.T) {
	dir := plugintest.EchoDir(t)
	out := run(t, `
metadata: {name: crash}
journeys:
  - name: j
    steps:
      - name: crash
        if: ${iter == 1}
        plugin: echo.crash
      - think: 1100ms
      - name: say
        plugin: echo.say
        with: {text: back}
load: {vus: 1, iterations: 3}`, func(o *Options) { o.PluginDir = dir })
	if c := stepNamed(t, out, "crash"); c.Errors["plugin crashed"] != 1 {
		t.Errorf("crash: %v", c.Errors)
	}
	if s := stepNamed(t, out, "say"); s.Requests != 2 || s.Failed != 0 {
		t.Errorf("say after the restart: %d requests, errors %v", s.Requests, s.Errors)
	}
}

func TestPluginProblemsFailBeforeTheRun(t *testing.T) {
	dir := plugintest.EchoDir(t)
	for _, tc := range []struct{ step, want string }{
		{`{plugin: nothere.say}`, "stampede plugin install nothere"},
		{`{plugin: echo.say, with: {count: 1}}`, "missing property 'text'"},
		{`{plugin: echo.yell}`, `plugin echo has no step "yell"`},
	} {
		s, err := scenario.Parse([]byte(`
metadata: {name: x}
journeys: [{name: j, steps: [` + tc.step + `]}]
load: {vus: 1, iterations: 1}`))
		if err != nil {
			t.Fatal(err)
		}
		prog, err := scenario.Compile(s)
		if err != nil {
			t.Fatal(err)
		}
		plan, _ := s.Load.Plan()
		_, err = New(Options{Program: prog, Plan: plan, PluginDir: dir})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.step, err, tc.want)
		}
	}
}

// pathLog records request paths from a test server's handlers.
type pathLog struct {
	mu   sync.Mutex
	vals []string
}

func (s *pathLog) add(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vals = append(s.vals, v)
}

func (s *pathLog) list() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.vals...)
}

func (s *pathLog) allHavePrefix(p string) bool {
	for _, v := range s.list() {
		if !strings.HasPrefix(v, p) {
			return false
		}
	}
	return true
}
