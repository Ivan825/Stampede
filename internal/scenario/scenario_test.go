package scenario

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseExample(t *testing.T) {
	b, err := os.ReadFile("testdata/shop.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Parse(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := len(s.Journeys); got != 2 {
		t.Fatalf("journeys = %d, want 2", got)
	}
	co := s.Journeys[1]
	if co.Steps[2].Name != "add to cart" || co.Steps[2].Request.Method != "POST" {
		t.Errorf("unexpected step: %+v", co.Steps[2])
	}
	if !co.Steps[2].Request.Check.Status.Match(201) || co.Steps[2].Request.Check.Status.Match(204) {
		t.Errorf("status matcher wrong: %v", co.Steps[2].Request.Check.Status)
	}
	if co.Steps[3].Kind != StepBranch || len(co.Steps[3].Branch) != 2 {
		t.Errorf("branch not parsed: %+v", co.Steps[3])
	}
	if s.Data["users"].Mode != FeedUnique || s.Data["queries"].Mode != FeedRandom {
		t.Errorf("feeder modes wrong: %+v", s.Data)
	}

	p, err := Compile(s)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// 5 thresholds: 3 global + p95 and errors from the checkout journey.
	if len(p.Thresholds) != 5 {
		t.Errorf("thresholds = %d, want 5: %+v", len(p.Thresholds), p.Thresholds)
	}
	if len(p.Steps) != 6 {
		t.Errorf("request steps = %d, want 6", len(p.Steps))
	}
	if p.TotalWeight != 65 {
		t.Errorf("total weight = %d", p.TotalWeight)
	}

	plan, err := s.Load.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if plan.Executor != ExecRampingRate || !plan.StopOnFail || plan.Peak() != 5000 {
		t.Errorf("unexpected plan: %+v", plan)
	}
}

func TestRoundTrip(t *testing.T) {
	b, _ := os.ReadFile("testdata/shop.yaml")
	s, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse failed: %v\n%s", err, out)
	}
	if len(s2.Journeys[1].Steps) != len(s.Journeys[1].Steps) {
		t.Fatalf("steps lost in round trip")
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]struct {
		src  string
		want string
	}{
		"undeclared var": {`
metadata: {name: t}
target: {baseURL: "http://localhost"}
journeys: [{name: a, steps: [{get: "/x/${missing}"}]}]
load: {vus: 1, duration: 1s}`, `is "missing" extracted in an earlier step`},
		"extract after use": {`
metadata: {name: t}
target: {baseURL: "http://localhost"}
journeys: [{name: a, steps: [{get: "/x/${id}"}, {get: /y, extract: {id: "$.id"}}]}]
load: {vus: 1, duration: 1s}`, `"id"`},
		"two actions": {`
metadata: {name: t}
target: {baseURL: "http://localhost"}
journeys: [{name: a, steps: [{get: /x, post: /y}]}]
load: {vus: 1, duration: 1s}`, "more than one action"},
		"unknown key": {`
metadata: {name: t}
target: {baseURL: "http://localhost"}
journeys: [{name: a, steps: [{get: /x, chekc: {status: 200}}]}]
load: {vus: 1, duration: 1s}`, `unknown key "chekc"`},
		"unknown check key": {`
metadata: {name: t}
target: {baseURL: "http://localhost"}
journeys:
  - name: a
    steps:
      - get: /x
        check: {stauts: 200}
load: {vus: 1, duration: 1s}`, `line 8: unknown key "stauts" in check`},
		"unknown top-level": {`
metadata: {name: t}
targte: {baseURL: "http://localhost"}`, "targte"},
		"bad threshold scope": {`
metadata: {name: t}
target: {baseURL: "http://localhost"}
journeys: [{name: a, steps: [{get: /x}]}]
load: {vus: 1, duration: 1s}
targets: ["nope.p95 < 1s"]`, `unknown journey or step "nope"`},
		"reserved extract": {`
metadata: {name: t}
target: {baseURL: "http://localhost"}
journeys: [{name: a, steps: [{get: /x, extract: {status: status}}]}]
load: {vus: 1, duration: 1s}`, "built-in name"},
		"missing base url": {`
metadata: {name: t}
journeys: [{name: a, steps: [{get: /x}]}]
load: {vus: 1, duration: 1s}`, "baseURL"},
		"no load": {`
metadata: {name: t}
target: {baseURL: "http://localhost"}
journeys: [{name: a, steps: [{get: /x}]}]`, "load"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.src))
			if err == nil {
				t.Fatalf("expected error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestBranchVariablesUnion(t *testing.T) {
	src := `
metadata: {name: t}
target: {baseURL: "http://localhost"}
journeys:
  - name: a
    steps:
      - branch:
          - weight: 1
            steps: [{get: /a, extract: {x: "$.x"}}]
          - weight: 1
            steps: [{get: /b, extract: {y: "$.y"}}]
      - get: /c/${x}/${y}
load: {vus: 1, duration: 1s}`
	if _, err := Parse([]byte(src)); err != nil {
		t.Fatal(err)
	}
}

func TestValidationErrorType(t *testing.T) {
	_, err := Parse([]byte("metadata: {name: BAD NAME}\njourneys: []\nload: {vus: 1, duration: 1s}"))
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) < 2 {
		t.Fatalf("want ValidationError with several problems, got %v", err)
	}
}

func TestUnits(t *testing.T) {
	d, _ := ParseDuration("1m30s")
	if d.D() != 90*time.Second {
		t.Error(d)
	}
	d, _ = ParseDuration("2d")
	if d.D() != 48*time.Hour {
		t.Error(d)
	}
	d, _ = ParseDuration("1.5")
	if d.D() != 1500*time.Millisecond {
		t.Error(d)
	}
	r, _ := ParseRate("3000/m")
	if r.PerSecond() != 50 {
		t.Error(r)
	}
	if _, err := ParseRate("5/x"); err == nil {
		t.Error("want error for bad unit")
	}
	p, _ := ParsePercent("0.5%")
	if p != 0.005 {
		t.Error(p)
	}
	tt, _ := ParseThinkTime("2s..6s")
	if tt.Min.D() != 2*time.Second || tt.Max.D() != 6*time.Second {
		t.Error(tt)
	}
	if _, err := ParseThinkTime("6s..2s"); err == nil {
		t.Error("want error for inverted range")
	}
}

func TestThresholds(t *testing.T) {
	cases := []struct {
		src    string
		scope  string
		metric string
		op     string
		value  float64
	}{
		{"http.p95 < 500ms", "http", "p95", "<", 0.5},
		{"errors < 1%", "http", "errors", "<", 0.01},
		{"checkout.p99.9 <= 2s", "checkout", "p99.9", "<=", 2},
		{"browse/GET /api/x.max < 1s", "browse/GET /api/x", "max", "<", 1},
		{"rps >= 100/s", "http", "rps", ">=", 100},
		{"checks > 99%", "http", "checks", ">", 0.99},
	}
	for _, c := range cases {
		th, err := ParseThreshold(c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if th.Scope != c.scope || th.Metric != c.metric || th.Op != c.op || th.Value != c.value {
			t.Errorf("%s: got %+v", c.src, th)
		}
	}
	for _, bad := range []string{"p95 500ms", "http.p42 < 1s", "errors < 200%", "p95 < fast"} {
		if _, err := ParseThreshold(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
	th, _ := ParseThreshold("p95 < 500ms")
	if !th.Pass(0.4) || th.Pass(0.5) {
		t.Error("Pass wrong")
	}
}

func TestJSONPath(t *testing.T) {
	cases := map[string]string{
		"$":              "@this",
		"$.items[0].id":  "items.0.id",
		"$.items[*].id":  "items.#.id",
		"$['a.b'].c":     `a\.b.c`,
		"$.items.length": "items.#",
		`$["x"][2]`:      "x.2",
	}
	for in, want := range cases {
		got, err := JSONPathToGJSON(in)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"items", "$.a[", "$.a[-1]", "$..a"} {
		if _, err := JSONPathToGJSON(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}
