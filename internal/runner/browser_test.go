package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/engine"
	"github.com/Ivan825/Stampede/internal/scenario"
)

func TestBrowserReport(t *testing.T) {
	if _, err := engine.FindChrome(); err != nil {
		t.Skip(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Shop</title><h1>Shop</h1><button id="b" onclick="document.body.append('clicked')">Go</button>`)
	}))
	defer srv.Close()
	s, err := scenario.Parse(fmt.Appendf(nil, `
metadata: {name: browser}
target: {baseURL: %q}
journeys:
  - name: visit
    steps:
      - browser: /
        steps:
          - click: "#b"
load: {iterations: 3, vus: 1}`, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), Options{Scenario: s, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	steps := rep.Journeys[0].Steps
	if len(steps) != 2 || rep.Overall.Failed != 0 {
		t.Fatalf("steps %d, failed %d, errors %v", len(steps), rep.Overall.Failed, rep.Errors)
	}
	load, click := steps[0].Browser, steps[1].Browser
	if load == nil || load.FCP == nil || load.FCP.Mean <= 0 || load.Load == nil || load.CLS == nil || load.TTFB == nil {
		t.Errorf("page load vitals = %+v", load)
	}
	if click != nil && click.Load != nil {
		t.Errorf("a click has no page load: %+v", click)
	}
	var txt, html bytes.Buffer
	rep.WriteText(&txt)
	if err := rep.WriteHTML(&html); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(txt.String(), "web vitals (mean / p95)") || !strings.Contains(html.String(), "<h2>Web vitals</h2>") {
		t.Errorf("reports must show web vitals:\n%s", txt.String())
	}
}
