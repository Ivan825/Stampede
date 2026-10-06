package scenario

import (
	"strings"
	"testing"

	"cel.dev/cel-go/interpreter"
)

func act(m map[string]any) interpreter.Activation {
	a, _ := interpreter.NewActivation(m)
	return a
}

func TestTemplateRender(t *testing.T) {
	sc, err := NewScope("id", "n")
	if err != nil {
		t.Fatal(err)
	}
	vars := act(map[string]any{
		"id":   "p-42",
		"n":    int64(3),
		"env":  map[string]string{"HOST": "shop.test"},
		"data": map[string]any{"users": map[string]any{"email": "a@b.c"}},
	})
	cases := map[string]string{
		"plain":                     "plain",
		"/p/${id}":                  "/p/p-42",
		"${n * 2}x":                 "6x",
		"https://${env.HOST}/a":     "https://shop.test/a",
		"${data.users.email}":       "a@b.c",
		"$${literal}":               "${literal}",
		`${ {"k": id}.k }`:          "p-42",
		"${n > 2 ? 'many' : 'few'}": "many",
		"${toJSON([1, 2])}":         "[1,2]",
	}
	for src, want := range cases {
		tp, err := sc.CompileTemplate(src)
		if err != nil {
			t.Errorf("%s: compile: %v", src, err)
			continue
		}
		got, err := tp.Render(vars)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", src, got, err, want)
		}
	}

	tp, _ := sc.CompileTemplate("${n}")
	v, _ := tp.Value(vars)
	if v != int64(3) {
		t.Errorf("Value should keep type, got %T %v", v, v)
	}
}

func TestTemplateFunctions(t *testing.T) {
	sc, _ := NewScope()
	vars := act(map[string]any{})
	for i := 0; i < 200; i++ {
		tp, _ := sc.CompileTemplate("${rand(5, 7)}")
		v, _ := tp.Value(vars)
		if n := v.(int64); n < 5 || n > 7 {
			t.Fatalf("rand out of range: %d", n)
		}
	}
	tp, _ := sc.CompileTemplate("${uuid()}")
	s, _ := tp.Render(vars)
	if len(s) != 36 || s[14] != '4' {
		t.Errorf("bad uuid %q", s)
	}
	tp, _ = sc.CompileTemplate("${randString(12)}")
	s, _ = tp.Render(vars)
	if len(s) != 12 {
		t.Errorf("bad randString %q", s)
	}
	tp, _ = sc.CompileTemplate("${pick(['a', 'b'])}")
	s, _ = tp.Render(vars)
	if s != "a" && s != "b" {
		t.Errorf("bad pick %q", s)
	}
	tp, _ = sc.CompileTemplate("${base64('hi')}|${urlencode('a b')}")
	s, _ = tp.Render(vars)
	if s != "aGk=|a+b" {
		t.Errorf("got %q", s)
	}
}

func TestTemplateErrors(t *testing.T) {
	sc, _ := NewScope()
	for _, bad := range []string{"${", "${}", "${nope}", "${1 +}"} {
		if _, err := sc.CompileTemplate(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestJSONTemplate(t *testing.T) {
	s, err := Parse([]byte(`
metadata: {name: t}
target: {baseURL: "http://localhost"}
vars: {qty: 2}
journeys:
  - name: a
    steps:
      - post: /x
        json: { id: "${qty}", name: "item-${qty}", nested: [1, "${qty + 1}"], fixed: true }
load: {vus: 1, duration: 1s}`))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.Steps[0].Req.JSON.Value(act(map[string]any{"qty": int64(2)}))
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["id"] != int64(2) || m["name"] != "item-2" || m["fixed"] != true {
		t.Errorf("got %#v", m)
	}
	if n := m["nested"].([]any); n[1] != int64(3) {
		t.Errorf("nested: %#v", n)
	}
}

func TestPlans(t *testing.T) {
	cases := []struct {
		yaml     string
		executor string
		peak     float64
		total    string
	}{
		{"vus: 10\nduration: 1m", ExecConstantVUs, 10, "1m0s"},
		{"mode: rate\nrate: 100/s\nduration: 30s", ExecConstantRate, 100, "30s"},
		{"iterations: 50\nvus: 5", ExecIterations, 0, "0s"},
		{"stages: [{duration: 1m, target: '20'}, {duration: 2m, target: '0'}]", ExecRampingVUs, 20, "3m0s"},
		{"shape: spike\nmode: rate\nstart: 10/s\nmax: 200/s", ExecRampingRate, 200, "9m20s"},
		{"shape: soak\nvus: 50\nduration: 1h", ExecRampingVUs, 50, "1h4m0s"},
		{"shape: steps\nmode: rate\nmax: 500/s\nsteps: 5\nstepDuration: 1m", ExecRampingRate, 500, "5m0s"},
		{"shape: wave\nvus: 10\nmax: '30'\ncycles: 2\nduration: 8m", ExecRampingVUs, 30, "9m0s"},
		{"shape: smoke", ExecRampingVUs, 2, "1m0s"},
		{"shape: stress\nvus: 100\nduration: 19m", ExecRampingVUs, 200, "19m0s"},
	}
	for _, c := range cases {
		s, err := Decode([]byte("load:\n  " + strings.ReplaceAll(c.yaml, "\n", "\n  ")))
		if err != nil {
			t.Fatalf("%s: %v", c.yaml, err)
		}
		p, err := s.Load.Plan()
		if err != nil {
			t.Errorf("%s: %v", c.yaml, err)
			continue
		}
		if p.Executor != c.executor {
			t.Errorf("%s: executor %s, want %s", c.yaml, p.Executor, c.executor)
		}
		if c.peak > 0 && p.Peak() != c.peak {
			t.Errorf("%s: peak %v, want %v", c.yaml, p.Peak(), c.peak)
		}
		if c.executor != ExecIterations && p.TotalDuration().String() != c.total {
			t.Errorf("%s: total %s, want %s", c.yaml, p.TotalDuration(), c.total)
		}
	}
}

func TestPlanValueAt(t *testing.T) {
	p := &Plan{Start: 0, Stages: []PlanStage{{Duration: 10e9, Target: 100}, {Duration: 10e9, Target: 100}}}
	if v := p.ValueAt(5e9); v != 50 {
		t.Errorf("mid-ramp = %v", v)
	}
	if v := p.ValueAt(15e9); v != 100 {
		t.Errorf("hold = %v", v)
	}
	if v := p.ValueAt(30e9); v != 100 {
		t.Errorf("after end = %v", v)
	}
}
