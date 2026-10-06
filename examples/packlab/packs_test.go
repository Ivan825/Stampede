package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
	"github.com/Ivan825/Stampede/internal/cli"
	"github.com/Ivan825/Stampede/internal/pack"
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
func TestPacksAgainstReferenceApps(t *testing.T) {
	shipped, err := pack.Shipped()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names() {
		p := products[name]
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(p.new(labkit.Config{Fast: true}))
			defer srv.Close()

			t.Run("detect", func(t *testing.T) { checkDetect(t, srv.URL, p.pack, shipped) })
			t.Run("pack test", func(t *testing.T) {
				out, err := stampede(t, "", "pack", "test", p.pack, "--target", srv.URL)
				if err != nil {
					t.Fatalf("pack test: %v\n%s", err, out)
				}
				if !strings.Contains(out, "Every journey in "+p.pack+" works") {
					t.Fatalf("unexpected output:\n%s", out)
				}
			})
			t.Run("load", func(t *testing.T) { checkLoad(t, srv.URL, p.pack) })
		})
	}
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
func checkLoad(t *testing.T, target, name string) {
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
			t.Logf("%d iterations, %d requests, p95 %.1fms", o.Iterations, o.Requests, o.Latency.P95*1000)
		})
	}
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
	limit := scenario.Duration(20 * time.Millisecond)
	for i := range steps {
		st := &steps[i]
		if st.Think != nil {
			st.Think.Min, st.Think.Max = min(st.Think.Min, limit), min(st.Think.Max, limit)
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
