package pluginhost_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pluginv1 "github.com/Ivan825/Stampede/gen/stampede/plugin/v1"
	"github.com/Ivan825/Stampede/internal/pluginhost"
	"github.com/Ivan825/Stampede/internal/pluginhost/plugintest"
	"github.com/Ivan825/Stampede/internal/scenario"
)

func TestMain(m *testing.M) {
	code := m.Run()
	plugintest.Remove()
	os.Exit(code)
}

func load(t *testing.T) *pluginhost.Plugin {
	t.Helper()
	dir := plugintest.EchoDir(t)
	set, err := pluginhost.Load(context.Background(), dir, []string{"echo"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(set.Kill)
	return set["echo"]
}

func exec(t *testing.T, s *pluginhost.Session, step, config string) *pluginv1.ExecuteResponse {
	t.Helper()
	r, err := s.Execute(context.Background(), &pluginv1.ExecuteRequest{Step: step, Config: []byte(config), TimeoutNs: int64(5 * time.Second)})
	if err != nil {
		t.Fatalf("%s: %v", step, err)
	}
	return r
}

func TestDescribeAndLifecycle(t *testing.T) {
	p := load(t)
	if p.Name != "echo" || p.Version != "1.0.0" {
		t.Fatalf("described as %s %s", p.Name, p.Version)
	}
	if got := strings.Join(p.StepNames(), ","); got != "closed,connect,crash,panic,say" {
		t.Fatalf("steps %s", got)
	}
	if got := p.Steps["say"].Targets; len(got) != 1 || got[0] != "addr" {
		t.Fatalf("say targets %v", got)
	}

	s, err := p.Open(context.Background(), 7, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	r := exec(t, s, "say", `{"text":"hi","count":2}`)
	if !r.GetOk() || r.GetLatencyNs() != int64(3*time.Millisecond) || r.GetPhasesNs()["wait"] != int64(2*time.Millisecond) {
		t.Fatalf("say: %+v", r)
	}
	var vals map[string]any
	if err := json.Unmarshal(r.GetValues(), &vals); err != nil {
		t.Fatal(err)
	}
	if vals["text"] != "hi" || vals["vu"] != float64(7) {
		t.Fatalf("values %v", vals)
	}

	if r := exec(t, s, "say", `{"text":"x","fail":"echo refused"}`); r.GetOk() || r.GetErrorClass() != "echo refused" {
		t.Fatalf("fail: %+v", r)
	}
	if r := exec(t, s, "say", `{"txt":"x"}`); r.GetOk() || r.GetErrorClass() != "invalid config" {
		t.Fatalf("invalid config: %+v", r)
	}
	if r := exec(t, s, "nope", `{}`); r.GetOk() || r.GetErrorClass() != "unknown step" {
		t.Fatalf("unknown step: %+v", r)
	}
	if r := exec(t, s, "panic", `{}`); r.GetOk() || r.GetErrorClass() != "plugin panic" {
		t.Fatalf("panic: %+v", r)
	}
	r, err = s.Execute(context.Background(), &pluginv1.ExecuteRequest{Step: "say", Config: []byte(`{"text":"x","sleep":"1s"}`), TimeoutNs: int64(50 * time.Millisecond)})
	if err != nil || r.GetOk() || r.GetErrorClass() != "echo timeout" {
		t.Fatalf("timeout: %+v %v", r, err)
	}
	if r := exec(t, s, "connect", `{}`); r.GetSkipped() {
		t.Fatal("first connect skipped")
	}
	if r := exec(t, s, "connect", `{}`); !r.GetSkipped() {
		t.Fatal("second connect not skipped")
	}
	s.Close(context.Background())
	if r := exec(t, s, "say", `{"text":"x"}`); r.GetOk() || r.GetErrorClass() != "unknown session" {
		t.Fatalf("closed session: %+v", r)
	}
	s2, _ := p.Open(context.Background(), 8, "run-1")
	var closed map[string]any
	_ = json.Unmarshal(exec(t, s2, "closed", `{}`).GetValues(), &closed)
	if closed["closed"] != float64(1) {
		t.Fatalf("session Close was not called: %v", closed)
	}
}

func TestConcurrentSessions(t *testing.T) {
	p := load(t)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for vu := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := p.Open(context.Background(), int64(vu), "r")
			if err != nil {
				errs <- err
				return
			}
			defer s.Close(context.Background())
			for range 20 {
				r, err := s.Execute(context.Background(), &pluginv1.ExecuteRequest{Step: "say", Config: []byte(`{"text":"x"}`), TimeoutNs: int64(time.Second)})
				if err != nil || !r.GetOk() {
					errs <- errors.New("execute failed")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestCrashRestarts(t *testing.T) {
	p := load(t)
	s, err := p.Open(context.Background(), 1, "r")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Execute(context.Background(), &pluginv1.ExecuteRequest{Step: "crash", Config: []byte(`{}`), TimeoutNs: int64(time.Second)})
	if !errors.Is(err, pluginhost.ErrCrashed) {
		t.Fatalf("crash: %v", err)
	}
	if !s.Stale() {
		t.Fatal("session should be stale after a crash")
	}
	// The plugin comes back on a later Open.
	deadline := time.Now().Add(5 * time.Second)
	var s2 *pluginhost.Session
	for time.Now().Before(deadline) {
		if s2, err = p.Open(context.Background(), 1, "r"); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("plugin did not restart: %v", err)
	}
	if r := exec(t, s2, "say", `{"text":"back"}`); !r.GetOk() {
		t.Fatalf("after restart: %+v", r)
	}
}

func TestFindAndList(t *testing.T) {
	dir := plugintest.EchoDir(t)
	p, err := pluginhost.Find(dir, "echo")
	if err != nil || filepath.Dir(p) != dir {
		t.Fatalf("find: %s %v", p, err)
	}
	if _, err := pluginhost.Find(t.TempDir(), "nothere"); !errors.Is(err, pluginhost.ErrNotInstalled) || !strings.Contains(err.Error(), "stampede plugin install nothere") {
		t.Fatalf("missing plugin: %v", err)
	}
	t.Setenv("PATH", "")
	list, err := pluginhost.List(dir)
	if err != nil || len(list) != 1 || list[0].Name != "echo" || !list[0].InDir {
		t.Fatalf("list: %+v %v", list, err)
	}
}

func TestLoadRefusesMisnamedPlugin(t *testing.T) {
	src := filepath.Join(plugintest.EchoDir(t), pluginhost.BinaryName("echo"))
	dir := t.TempDir()
	b, _ := os.ReadFile(src)
	if err := os.WriteFile(filepath.Join(dir, pluginhost.BinaryName("other")), b, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := pluginhost.Load(context.Background(), dir, []string{"other"}, nil)
	if err == nil || !strings.Contains(err.Error(), `describes itself as plugin "echo"`) {
		t.Fatalf("got %v", err)
	}
}

func TestCheckProgram(t *testing.T) {
	p := load(t)
	set := pluginhost.Set{"echo": p}
	tests := []struct {
		with string
		want string // "" for valid
	}{
		{`{text: hi, count: 2}`, ""},
		{`{text: "${vu}", count: "${iter}"}`, ""}, // templated values are checked at run time
		{`{text: hi, mode: "${env.MODE}"}`, ""},
		{`{text: hi, tags: ["${vu}", b]}`, ""},
		{`{count: 2}`, "missing property 'text'"},
		{`{text: hi, cuont: 2}`, "cuont"},
		{`{text: hi, count: many}`, ".count: got string, want integer"},
		{`{text: hi, count: -1}`, ".count: minimum"},
		{`{text: hi, mode: shout}`, ".mode: value must be one of"},
		{`{text: "${vu}", extra: 1}`, "extra"},
	}
	for _, tc := range tests {
		src := `
metadata: {name: t}
journeys:
  - name: j
    steps:
      - plugin: echo.say
        with: ` + tc.with + `
load: {vus: 1, iterations: 1}`
		sc, err := scenario.Parse([]byte(src))
		if err != nil {
			t.Fatalf("%s: %v", tc.with, err)
		}
		prog, err := scenario.Compile(sc)
		if err != nil {
			t.Fatalf("%s: %v", tc.with, err)
		}
		err = pluginhost.CheckProgram(prog, set)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: unexpected %v", tc.with, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: got %v, want %q", tc.with, err, tc.want)
		}
	}

	sc, _ := scenario.Parse([]byte(`
metadata: {name: t}
journeys: [{name: j, steps: [{plugin: echo.shout, with: {}}]}]
load: {vus: 1, iterations: 1}`))
	prog, _ := scenario.Compile(sc)
	if err := pluginhost.CheckProgram(prog, set); err == nil || !strings.Contains(err.Error(), `plugin echo has no step "shout" (it has closed, connect, crash, panic, say)`) {
		t.Errorf("unknown step: %v", err)
	}
}

func TestTargetHosts(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{"broker:1883", "broker"},
		{"tcp://10.0.0.5:1883", "10.0.0.5"},
		{"mqtts://iot.example.com", "iot.example.com"},
		{"[::1]:9092", "::1"},
		{[]any{"k1:9092", "k2:9092"}, "k1,k2"},
		{"postgres://app:secret@db1:5432,db2:5432/shop?sslmode=disable", "db1,db2"},
		{"host=pg.internal port=5432 user=app", "pg.internal"},
		{"app:pw@tcp(mysql:3306)/shop", "mysql"},
		{"app:pw@/shop", "localhost"},
		{"app:pw@unix(/tmp/mysql.sock)/shop", "localhost"},
		{"postgres:///shop?host=/var/run/postgresql", "localhost"},
		{"redis://:pw@cache:6379/0", "cache"},
	}
	for _, tc := range tests {
		got, err := pluginhost.TargetHosts(tc.in)
		if err != nil || strings.Join(got, ",") != tc.want {
			t.Errorf("%v: got %v %v, want %s", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []any{"user=app password=x", 42, []any{1}} {
		if _, err := pluginhost.TargetHosts(bad); err == nil {
			t.Errorf("%v: want an error", bad)
		}
	}
}
