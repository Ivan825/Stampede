package cli

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/scenario"
)

// TestConsoleOptions checks the hooks the terminal console gets: /init
// runs stampede init without asking, and local runs get run's safety
// rules.
func TestConsoleOptions(t *testing.T) {
	srv := target(t)
	o := consoleOptions()
	var out bytes.Buffer
	if err := o.Init(context.Background(), &out, srv.URL, t.TempDir(), nil); err != nil || !strings.Contains(out.String(), "No shipped pack matches this target") {
		t.Errorf("init: %v %s", err, out.String())
	}
	if err := o.Init(context.Background(), &out, "not a url", "", nil); err == nil {
		t.Error("init accepted a bad target")
	}

	s, err := scenario.Parse([]byte("metadata: {name: x}\ntarget: {baseURL: \"${env.TARGET_URL}\"}\njourneys: [{name: a, steps: [{get: /}]}]\nload: {iterations: 1}\n"))
	if err != nil {
		t.Fatal(err)
	}
	allow, err := o.CheckTarget(context.Background(), &out, s, map[string]string{"TARGET_URL": srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(srv.URL)
	other, _ := url.Parse("https://example.com/")
	if !allow(u) || allow(other) {
		t.Errorf("policy allows target %v, other host %v", allow(u), allow(other))
	}
	if _, err := o.CheckTarget(context.Background(), &out, s, map[string]string{}); err == nil {
		t.Error("an unset base URL was accepted")
	}
}
