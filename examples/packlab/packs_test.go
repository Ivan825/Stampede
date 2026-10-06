package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"maps"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
	"github.com/Ivan825/Stampede/internal/cli"
	"github.com/Ivan825/Stampede/internal/pack"
	"github.com/Ivan825/Stampede/internal/pluginhost"
	"github.com/Ivan825/Stampede/internal/runner"
	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// Every product pack backed by a PackLab app must pass three checks
// against it, which is what makes the pack "shipped":
//
//  1. stampede init's probe picks that pack for the app;
//  2. stampede pack test dry-runs every journey once without an error;
//  3. every journey and stress file runs under real load (shortened) with
//     no failed request and no failed iteration.
//
// Packs whose scenarios use protocol plugins (mqtt, kafka, redis, sql,
// udp) run with the plugins built from plugins/ in this checkout. DBLab
// needs a PostgreSQL database: STAMPEDE_TEST_POSTGRES_DSN, as for the SQL
// plugin's tests; without it the databases pack is skipped.
func TestPacksAgainstReferenceApps(t *testing.T) {
	shipped, err := pack.Shipped()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names() {
		p := products[name]
		t.Run(name, func(t *testing.T) {
			cfg := labkit.Config{Fast: true, Listen: "127.0.0.1:0", Postgres: os.Getenv("STAMPEDE_TEST_POSTGRES_DSN")}
			if p.postgres && cfg.Postgres == "" {
				t.Skip("set STAMPEDE_TEST_POSTGRES_DSN to a PostgreSQL database to test this pack")
			}
			if len(p.plugins) > 0 {
				t.Setenv(pluginhost.DirEnv, pluginDir(t, p.plugins))
			}
			app, err := p.start(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			srv := httptest.NewServer(app.Handler)
			defer srv.Close()

			t.Run("detect", func(t *testing.T) {
				pk, err := pack.Load(p.pack)
				if err != nil {
					t.Fatal(err)
				}
				if !detectable(pk) {
					t.Skip("the pack has nothing stampede init can detect over HTTP")
				}
				checkDetect(t, srv.URL, p.pack, shipped)
				if len(app.Env) == 0 {
					return
				}
				// Without the broker's address init installs the pack and
				// says what else it needs instead of failing the dry run.
				out, err := stampede(t, "", "init", "--yes", "--target", srv.URL, "--dir", t.TempDir())
				if err != nil {
					t.Fatalf("init: %v\n%s", err, out)
				}
				for k := range app.Env {
					if !strings.Contains(out, "  "+k+": ") {
						t.Fatalf("init did not ask for %s:\n%s", k, out)
					}
				}
			})
			t.Run("pack test", func(t *testing.T) {
				// The pack's own files with think times cut to 10ms, so
				// the dry run takes seconds; the CI packs job runs the
				// unmodified pack against the packlab binary.
				args := []string{"pack", "test", quickCopy(t, p.pack), "--target", srv.URL}
				for _, k := range slices.Sorted(maps.Keys(app.Env)) {
					args = append(args, "-e", k+"="+app.Env[k])
				}
				out, err := stampede(t, "", args...)
				if err != nil {
					t.Fatalf("pack test: %v\n%s", err, out)
				}
				if !strings.Contains(out, "Every journey in "+p.pack+" works") {
					t.Fatalf("unexpected output:\n%s", out)
				}
			})
			t.Run("load", func(t *testing.T) { checkLoad(t, srv.URL, p.pack, app.Env) })
		})
	}
}

// detectable reports whether a pack gives stampede init anything to look
// for. A database has no HTTP side to probe.
func detectable(p *pack.Pack) bool {
	d := p.Detect
	return len(d.Paths)+len(d.OpenAPITags)+len(d.HTMLMeta)+len(d.Headers) > 0
}

var (
	pluginMu    sync.Mutex
	pluginsDir  string
	pluginBuilt = map[string]bool{}
)

// pluginDir builds the named plugins from plugins/<name> (each its own Go
// module) into one directory, once per test binary, and returns it.
func pluginDir(t *testing.T, names []string) string {
	t.Helper()
	pluginMu.Lock()
	defer pluginMu.Unlock()
	if pluginsDir == "" {
		d, err := os.MkdirTemp("", "packlab-plugins-")
		if err != nil {
			t.Fatal(err)
		}
		pluginsDir = d
	}
	for _, n := range names {
		if pluginBuilt[n] {
			continue
		}
		cmd := exec.Command("go", "build", "-o", filepath.Join(pluginsDir, pluginhost.BinaryName(n)), ".") //nolint:gosec // builds a first-party plugin
		cmd.Dir = filepath.Join("..", "..", "plugins", n)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building the %s plugin: %v\n%s", n, err, out)
		}
		pluginBuilt[n] = true
	}
	return pluginsDir
}

func TestMain(m *testing.M) {
	code := m.Run()
	if pluginsDir != "" {
		_ = os.RemoveAll(pluginsDir)
	}
	os.Exit(code)
}

// Every product the catalogue lists as shipped (apart from e-commerce,
// whose reference app is ShopLab) must have a PackLab app.
func TestShippedPacksHaveAnApp(t *testing.T) {
	shipped, err := pack.Shipped()
	if err != nil {
		t.Fatal(err)
	}
	for _, sp := range shipped {
		if sp.Name == "ecommerce" {
			continue
		}
		if _, ok := products[sp.Name]; !ok {
			t.Errorf("pack %s is shipped but packlab has no app for it", sp.Name)
		}
		if !strings.HasPrefix(sp.ReferenceApp, "examples/packlab/") {
			t.Errorf("pack %s: referenceApp %q", sp.Name, sp.ReferenceApp)
		}
	}
	for n, p := range products {
		if n != p.pack {
			t.Errorf("product %s serves pack %s", n, p.pack)
		}
	}
}

func checkDetect(t *testing.T, target, want string, shipped []*pack.Pack) {
	u, _ := url.Parse(target)
	ev, err := pack.Probe(context.Background(), u, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := pack.Score(ev, shipped)
	if len(m) == 0 || m[0].Pack.Name != want {
		t.Fatalf("best match is not %s: %+v", want, summary(m))
	}
	if len(m) > 1 && m[1].Score >= m[0].Score {
		t.Fatalf("%s ties with %s: %+v", want, m[1].Pack.Name, summary(m))
	}
	// The same through the command: init proposes the pack; answering no
	// installs nothing.
	out, err := stampede(t, "n\n", "init", "--target", target, "--dir", t.TempDir())
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Set up the "+want+" pack") {
		t.Fatalf("init did not propose %s:\n%s", want, out)
	}
}

var thinkLine = regexp.MustCompile(`(?m)^(\s*(?:- )?think:\s*).*$`)

// quickCopy installs a pack into a temporary folder with every think time
// set to 10ms and returns the pack's folder.
func quickCopy(t *testing.T, name string) string {
	t.Helper()
	p, err := pack.Load(name)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := p.Install(dir, false); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, name)
	files, _ := p.Files()
	for _, f := range files {
		fp := filepath.Join(root, filepath.FromSlash(f))
		b, err := os.ReadFile(fp)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, thinkLine.ReplaceAll(b, []byte("${1}10ms")), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func summary(ms []pack.Match) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Pack.Name+"="+strings.Join(m.Reasons, "|"))
	}
	return out
}

func stampede(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	root := cli.NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	err := root.ExecuteContext(ctx)
	return out.String(), err
}

// checkLoad runs every scenario of the pack under load, shortened so the
// test stays quick: think times are capped, durations are cut to a couple
// of seconds and user counts are capped.
func checkLoad(t *testing.T, target, name string, appEnv map[string]string) {
	p, err := pack.Load(name)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := p.Install(dir, false); err != nil {
		t.Fatal(err)
	}
	files, err := p.Files()
	if err != nil || len(files) == 0 {
		t.Fatalf("no scenario files: %v", err)
	}
	u, _ := url.Parse(target)
	policy := safety.NewHostPolicy(u.Hostname(), nil)
	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			s, err := scenario.LoadFile(filepath.Join(dir, name, filepath.FromSlash(f)))
			if err != nil {
				t.Fatal(err)
			}
			shorten(s)
			if err := s.Validate(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			env := map[string]string{"TARGET_URL": target}
			maps.Copy(env, appEnv)
			rep, err := runner.Run(ctx, runner.Options{
				Scenario: s, Env: env, Secrets: env, AllowHost: policy.Allow,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			if err != nil {
				t.Fatal(err)
			}
			o := rep.Overall
			if o.Iterations == 0 || o.Requests == 0 {
				t.Fatalf("no traffic: %+v", o)
			}
			if o.Failed > 0 || o.IterationsFailed > 0 {
				var errs []string
				for _, e := range rep.Errors {
					errs = append(errs, e.Error+" at "+e.Step)
				}
				t.Fatalf("%d of %d requests and %d iterations failed: %s", o.Failed, o.Requests, o.IterationsFailed, strings.Join(errs, "; "))
			}
			if code, ok := wantStatus[name+"/"+f]; ok && o.Status[code] == 0 {
				t.Fatalf("the stress never provoked a %d: %v", code, o.Status)
			}
			t.Logf("%d iterations, %d requests, p95 %.1fms, statuses %v", o.Iterations, o.Requests, o.Latency.P95*1000, o.Status)
		})
	}
}

// wantStatus lists stresses whose point is a refusal; under load they must
// actually provoke it, or they test nothing.
var wantStatus = map[string]int{
	"public-apis/stresses/rate-limit-burst.yaml":   429,
	"ticketing/stresses/seat-lock-contention.yaml": 409,
}

func shorten(s *scenario.Scenario) {
	for i := range s.Journeys {
		shortenSteps(s.Journeys[i].Steps)
	}
	l := &s.Load
	l.GracefulStop = scenario.Duration(20 * time.Second)
	l.Abort = nil
	if l.Iterations > 0 {
		l.Iterations = min(l.Iterations, 60)
		l.VUs = min(l.VUs, 20)
		return
	}
	if len(l.Stages) > 0 {
		for i := range l.Stages {
			l.Stages[i].Duration = scenario.Duration(time.Second)
			if n, err := strconv.Atoi(l.Stages[i].Target); err == nil && n > 50 {
				l.Stages[i].Target = "50"
			}
		}
		return
	}
	l.Duration = scenario.Duration(2 * time.Second)
	// Low everyday rates would start only a handful of journeys in two
	// seconds; raise them so every weighted journey gets traffic.
	if l.Shape == "" && l.Rate > 0 && l.Rate < 25 {
		l.Rate = 25
	}
	if l.VUs > 20 {
		l.VUs = 20
	}
}

func shortenSteps(steps []scenario.Step) {
	// A tenth of the pack's think time, at most 300ms: short enough for a
	// quick test, long enough that users still pause between messages.
	limit := scenario.Duration(300 * time.Millisecond)
	for i := range steps {
		st := &steps[i]
		if st.Think != nil {
			st.Think.Min, st.Think.Max = min(st.Think.Min/10, limit), min(st.Think.Max/10, limit)
		}
		if st.Loop != nil {
			shortenSteps(st.Loop.Steps)
		}
		if st.Group != nil {
			shortenSteps(st.Group.Steps)
		}
		if st.WS != nil {
			shortenSteps(st.WS.Steps)
		}
		for j := range st.Branch {
			shortenSteps(st.Branch[j].Steps)
		}
	}
}
