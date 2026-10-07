package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/api/gen"
)

// waitRun polls a run until it has finished.
func waitRun(t *testing.T, id string) gen.Run {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		var r gen.Run
		cliJSON(t, &r, "runs", "show", id)
		switch r.Status {
		case gen.Completed, gen.Aborted, gen.Failed:
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s still %s", id, r.Status)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestRunsCommands(t *testing.T) {
	c := signedIn(t)
	shop(t, c, target(t).URL)
	id := strings.TrimSpace(mustCLI(t, "start", "--scenario", "items", "--duration", "2s", "--note", "cli test", "--detach"))
	if len(id) != 36 {
		t.Fatalf("start: %q", id)
	}
	r := waitRun(t, id[:8])
	if r.Status != gen.Completed || r.Verdict == nil || *r.Verdict != gen.NoTargets {
		t.Fatalf("run: %+v", r)
	}

	// stampede runs still lists; runs list too, with --json and filters.
	for _, args := range [][]string{{"runs"}, {"runs", "list", "--scenario", "items"}, {"runs", "--limit", "5"}} {
		out := mustCLI(t, args...)
		if !strings.Contains(out, id[:8]) || !strings.Contains(out, "items v1") || !strings.Contains(out, "completed") || !strings.Contains(out, "no-targets") {
			t.Errorf("%v:\n%s", args, out)
		}
	}
	var runs []gen.Run
	cliJSON(t, &runs, "runs", "list")
	if len(runs) != 1 || runs[0].Id.String() != id {
		t.Errorf("runs list --json: %+v", runs)
	}
	if _, err := runCLI(t, "runs", "bogus"); err == nil {
		t.Error("runs accepted an unknown argument")
	}

	out := mustCLI(t, "runs", "show", id[:8])
	for _, want := range []string{"Run " + id, "Scenario:  items v1", "Status:    completed, verdict no-targets", "Results:   ", "Note:      cli test", "Report:    stampede report " + id[:8]} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}

	out = mustCLI(t, "runs", "timeline", id)
	if !strings.HasPrefix(out, "T   RPS  P50") || !strings.Contains(out, "0s ") {
		t.Errorf("timeline:\n%s", out)
	}
	var ps []gen.Point
	cliJSON(t, &ps, "runs", "timeline", id, "--resolution", "10s")
	if len(ps) == 0 || ps[0].T != 0 {
		t.Errorf("timeline --resolution 10s: %+v", ps)
	}
	if _, err := runCLI(t, "runs", "timeline", id, "--resolution", "7s"); err == nil {
		t.Error("an unknown resolution was accepted")
	}

	var es []gen.RunEvent
	cliJSON(t, &es, "runs", "events", id)
	if out := mustCLI(t, "runs", "events", id); len(es) == 0 && !strings.Contains(out, "No events") || len(es) > 0 && !strings.Contains(out, es[0].Type) {
		t.Errorf("events %+v:\n%s", es, out)
	}
	if out := mustCLI(t, "runs", "workers", id); !strings.Contains(out, "no live worker health") {
		t.Errorf("workers of a finished run: %s", out)
	}
	var rw gen.RunWorkers
	cliJSON(t, &rw, "runs", "workers", id)
	if rw.Live {
		t.Errorf("workers --json: %+v", rw)
	}

	// The kill switch for every run: start a long one, then kill --all.
	long := strings.TrimSpace(mustCLI(t, "start", "--scenario", "items", "--duration", "5m", "--detach"))
	if out := mustCLI(t, "kill", "--all"); out != "killed 1 run(s)\n" {
		t.Errorf("kill --all: %q", out)
	}
	if r := waitRun(t, long); r.Status == gen.Completed && r.StopReason == nil {
		t.Errorf("killed run: %+v", r)
	}
	if out := mustCLI(t, "workers", "--json"); strings.TrimSpace(out) != "[]" {
		t.Errorf("workers --json: %q", out)
	}
}
