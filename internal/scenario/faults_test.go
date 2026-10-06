package scenario

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestFaultsExampleParses(t *testing.T) {
	b, err := os.ReadFile("testdata/faults.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	tl := s.Faults.Timeline
	if len(tl) != 4 || tl[0].At.D() != time.Minute || tl[0].Latency.D() != 200*time.Millisecond || *tl[3].Replicas != 1 {
		t.Fatalf("timeline = %+v", tl)
	}
	for i, want := range []string{"slow database", "cache: reset", "pause shop-search", "scale shop/api to 1"} {
		if got := tl[i].Label(); got != want {
			t.Errorf("label %d = %q, want %q", i, got, want)
		}
	}
}

func TestFaultsValidation(t *testing.T) {
	base := `
metadata: {name: t}
target: {baseURL: "http://localhost"}
journeys: [{name: a, steps: [{get: /}]}]
load: {vus: 1, duration: 1s}
faults:
  agent: {url: "http://agent:7070", token: t}
  timeline:
`
	for _, c := range []struct{ step, want string }{
		{`[{at: 1s, proxy: db, latency: 10ms}]`, "how long the fault lasts"},
		{`[{at: 1s, for: 1s}]`, "exactly one of proxy, container or deployment"},
		{`[{at: 1s, for: 1s, proxy: db, container: x}]`, "exactly one of proxy, container or deployment"},
		{`[{at: 1s, for: 1s, proxy: db}]`, "needs latency, jitter, bandwidth"},
		{`[{at: 1s, for: 1s, proxy: db, bandwidth: fast}]`, "bandwidth"},
		{`[{at: 1s, for: 1s, container: x, action: explode}]`, "pause, stop, kill or restart"},
		{`[{at: 1s, for: 1s, container: x, action: pause, latency: 1s}]`, "takes only action"},
		{`[{at: 1s, for: 1s, deployment: api, replicas: 1}]`, "namespace/name"},
		{`[{at: 1s, for: 1s, deployment: shop/api}]`, "replicas"},
		{`[]`, "at least one fault"},
	} {
		_, err := Parse([]byte(base + "    " + c.step))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.step, err, c.want)
		}
	}
	if _, err := Parse([]byte(strings.Replace(base, `url: "http://agent:7070"`, `url: "agent:7070"`, 1) + "    [{at: 1s, for: 1s, proxy: db, reset: true}]")); err == nil || !strings.Contains(err.Error(), "absolute http(s) URL") {
		t.Errorf("relative agent url: %v", err)
	}
	if _, err := Parse([]byte(base + "    [{at: 1s, for: 1s, proxy: db, reset: true}]")); err != nil {
		t.Errorf("valid: %v", err)
	}
}
