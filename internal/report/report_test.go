package report

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/scenario"
)

func build(t *testing.T, latency time.Duration, failEvery int) *Report {
	t.Helper()
	s, err := scenario.Parse([]byte(`
metadata: {name: rep}
target: {baseURL: "http://localhost"}
journeys:
  - name: shop
    steps: [{get: /a}, {get: /b}]
load: {vus: 2, duration: 3s}
targets: ["http.p95 < 100ms", "errors < 5%", "shop/GET /b.p99 < 200ms"]`))
	if err != nil {
		t.Fatal(err)
	}
	prog, _ := scenario.Compile(s)
	plan, _ := s.Load.Plan()
	t0 := time.Unix(1_700_000_000, 0)
	var snaps []*metrics.Snapshot
	for i := int64(0); i < 3; i++ {
		sn := metrics.NewSnapshot(i)
		for n := 0; n < 100; n++ {
			for step := 0; step < 2; step++ {
				sm := &metrics.Sample{Step: step, Start: t0, End: t0.Add(latency), Status: 200}
				if failEvery > 0 && n%failEvery == 0 {
					sm.Failed, sm.Err, sm.Status = true, "HTTP 500", 500
				}
				sn.Step(step).Add(sm)
			}
			sn.Journey(0).Completed++
		}
		sn.VUs = 2
		snaps = append(snaps, sn)
	}
	return Build(Input{Program: prog, Plan: plan, Started: t0, Ended: t0.Add(3 * time.Second), StopReason: "completed", Snapshots: snaps})
}

func TestBuildPass(t *testing.T) {
	r := build(t, 20*time.Millisecond, 0)
	if r.Verdict != VerdictPass {
		t.Fatalf("verdict %s: %+v", r.Verdict, r.Thresholds)
	}
	if r.Overall.Requests != 600 || r.Overall.RPS != 200 || r.Overall.Iterations != 300 {
		t.Errorf("overall %+v", r.Overall)
	}
	if len(r.Timeline) != 3 || r.Timeline[0].RPS != 200 {
		t.Errorf("timeline %+v", r.Timeline)
	}
	if len(r.Journeys) != 1 || len(r.Journeys[0].Steps) != 2 {
		t.Fatalf("journeys %+v", r.Journeys)
	}
}

func TestBuildFail(t *testing.T) {
	r := build(t, 150*time.Millisecond, 10)
	if r.Verdict != VerdictFail {
		t.Fatalf("verdict %s", r.Verdict)
	}
	byName := map[string]Check{}
	for _, c := range r.Thresholds {
		byName[c.Source] = c
	}
	if byName["http.p95 < 100ms"].Pass || byName["errors < 5%"].Pass || !byName["shop/GET /b.p99 < 200ms"].Pass {
		t.Errorf("unexpected results: %+v", r.Thresholds)
	}
	if len(r.Errors) != 2 || r.Errors[0].Error != "HTTP 500" {
		t.Errorf("errors %+v", r.Errors)
	}
}

func TestExports(t *testing.T) {
	r := build(t, 150*time.Millisecond, 10)
	var buf bytes.Buffer
	if err := r.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := ReadJSON(&buf)
	if err != nil || back.Overall.Requests != r.Overall.Requests {
		t.Fatalf("json round trip: %v", err)
	}
	buf.Reset()
	if err := r.WriteHTML(&buf); err != nil {
		t.Fatal(err)
	}
	h := buf.String()
	for _, want := range []string{"<svg", "FAIL", "http.p95 &lt; 100ms", "GET /b"} {
		if !strings.Contains(h, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	if strings.Contains(h, "http://") && strings.Contains(h, "<script src") {
		t.Error("HTML report must not load external scripts")
	}
	buf.Reset()
	if err := r.WriteJUnit(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `failures="2"`) {
		t.Errorf("junit: %s", buf.String())
	}
	buf.Reset()
	r.WriteMarkdown(&buf)
	if !strings.Contains(buf.String(), "| `errors < 5%` |") {
		t.Errorf("markdown: %s", buf.String())
	}
	buf.Reset()
	r.WriteText(&buf)
	if !strings.Contains(buf.String(), "✗ http.p95 < 100ms") {
		t.Errorf("text: %s", buf.String())
	}
	if strings.Contains(buf.String(), "\x1b[") {
		t.Error("plain text has colour codes")
	}
	buf.Reset()
	r.WriteTextColor(&buf)
	for _, want := range []string{"\x1b[1;97;41m FAIL \x1b[0m", "\x1b[31m✗\x1b[0m http.p95 < 100ms"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("colour text lacks %q:\n%q", want, buf.String())
		}
	}
}

func TestNoDataIsNotPass(t *testing.T) {
	s, _ := scenario.Parse([]byte(`
metadata: {name: nodata}
target: {baseURL: "http://localhost"}
journeys: [{name: a, steps: [{get: /}]}]
load: {vus: 1, duration: 1s}
targets: ["p95 < 1s"]`))
	prog, _ := scenario.Compile(s)
	plan, _ := s.Load.Plan()
	r := Build(Input{Program: prog, Plan: plan})
	if r.Verdict != VerdictFail || r.Thresholds[0].ObservedText != "no data" {
		t.Errorf("a target with no data must fail: %+v", r.Thresholds)
	}
}

func TestBreakpointVerdict(t *testing.T) {
	r := build(t, 150*time.Millisecond, 10) // fails its targets overall
	if r.Verdict != VerdictFail {
		t.Fatal("precondition: overall targets fail")
	}
	s, _ := scenario.Parse([]byte(`
metadata: {name: rep}
target: {baseURL: "http://localhost"}
journeys: [{name: shop, steps: [{get: /a}]}]
load: {vus: 2, duration: 3s}
targets: ["http.p95 < 100ms"]`))
	prog, _ := scenario.Compile(s)
	plan, _ := s.Load.Plan()
	for _, c := range []struct {
		bp   *Breakpoint
		want string
	}{
		{&Breakpoint{Found: true, LastPass: 200, FirstFail: 300}, VerdictPass},
		{&Breakpoint{Found: true, LastPass: 0, FirstFail: 100}, VerdictFail},
	} {
		rep := Build(Input{Program: prog, Plan: plan, Breakpoint: c.bp, StopReason: "breakpoint reached",
			Snapshots: []*metrics.Snapshot{metrics.NewSnapshot(0)}})
		if rep.Verdict != c.want {
			t.Errorf("breakpoint %+v: verdict %s, want %s", c.bp, rep.Verdict, c.want)
		}
	}
}

func TestStreamStats(t *testing.T) {
	s, err := scenario.Parse([]byte(`
metadata: {name: stream}
target: {baseURL: "http://localhost"}
journeys: [{name: chat, steps: [{get: /a}, {get: /b}]}]
load: {vus: 1, duration: 1s}`))
	if err != nil {
		t.Fatal(err)
	}
	prog, _ := scenario.Compile(s)
	plan, _ := s.Load.Plan()
	t0 := time.Unix(1_700_000_000, 0)
	sn := metrics.NewSnapshot(0)
	phases := map[int]*[metrics.NumPhases]*metrics.Histogram{1: {}}
	for i := range phases[1] {
		phases[1][i] = metrics.NewHistogram()
	}
	for n := 0; n < 10; n++ {
		sn.Step(0).Add(&metrics.Sample{Step: 0, Start: t0, End: t0.Add(time.Millisecond), Status: 200, Proto: "HTTP/2.0"})
		// Each stream: first event after 200ms, then 20 more over 2s.
		sm := &metrics.Sample{Step: 1, Start: t0, End: t0.Add(2200 * time.Millisecond), Status: 200,
			Events: 21, StreamTime: 2 * time.Second}
		sm.Phases[metrics.PhaseFirstEvent] = 200 * time.Millisecond
		sn.Step(1).Add(sm)
		phases[1][metrics.PhaseFirstEvent].RecordDuration(sm.Phases[metrics.PhaseFirstEvent])
	}
	r := Build(Input{Program: prog, Plan: plan, Started: t0, Ended: t0.Add(time.Second), Snapshots: []*metrics.Snapshot{sn}, Phases: phases})
	steps := r.Journeys[0].Steps
	if steps[0].Stream != nil || steps[0].Protocols["HTTP/2.0"] != 10 {
		t.Errorf("plain step: stream %+v protocols %v", steps[0].Stream, steps[0].Protocols)
	}
	if _, ok := steps[0].Phases["firstEvent"]; ok {
		t.Error("the first-event phase belongs under stream, not phases")
	}
	st := steps[1].Stream
	if st == nil || st.Streams != 10 || st.Events != 210 {
		t.Fatalf("stream %+v", st)
	}
	if st.EventsPerSec != 10 {
		t.Errorf("events/s = %v, want 10", st.EventsPerSec)
	}
	if d := st.FirstEvent.P95 - 0.2; d < -0.002 || d > 0.002 || st.FirstEvent.Mean != 0.2 {
		t.Errorf("first event %+v, want 200ms", st.FirstEvent)
	}
	var buf bytes.Buffer
	r.WriteText(&buf)
	if !strings.Contains(buf.String(), "events/s") || !strings.Contains(buf.String(), "10.0") {
		t.Errorf("text report lacks stream figures:\n%s", buf.String())
	}
	buf.Reset()
	if err := r.WriteHTML(&buf); err != nil || !strings.Contains(buf.String(), "First event p95") {
		t.Errorf("HTML report lacks the streams table (%v)", err)
	}
}

func TestCSV(t *testing.T) {
	r := build(t, 150*time.Millisecond, 10)
	var buf bytes.Buffer
	if err := r.WriteCSV(&buf); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	// Header, two steps, the journey and the run.
	if len(rows) != 5 {
		t.Fatalf("%d rows: %v", len(rows), rows)
	}
	if got := rows[2][:4]; got[0] != "shop" || got[1] != "GET /b" || got[2] != "300" || got[3] != "30" {
		t.Errorf("step row %v", got)
	}
	if got := rows[4]; got[0] != "" || got[1] != "" || got[2] != "600" || got[8] != "150.000" {
		t.Errorf("run row %v", got)
	}
	buf.Reset()
	if err := r.WriteTimelineCSV(&buf); err != nil {
		t.Fatal(err)
	}
	rows, err = csv.NewReader(&buf).ReadAll()
	if err != nil || len(rows) != 4 || rows[1][1] != "200.000" {
		t.Fatalf("timeline %v %v", err, rows)
	}
}

func TestHTMLShowsBrowserArtifacts(t *testing.T) {
	r := build(t, 150*time.Millisecond, 10)
	r.Errors[0].Examples = []metrics.ErrorExample{{Request: "BROWSER http://shop.test/", Screenshot: []byte{0xFF, 0xD8, 0xFF}, Console: []string{"error: boom"}, HAR: `{"log":{}}`}}
	var buf bytes.Buffer
	if err := r.WriteHTML(&buf); err != nil {
		t.Fatal(err)
	}
	h := buf.String()
	for _, want := range []string{`src="data:image/jpeg;base64,/9j/`, "error: boom", `href="data:application/json;base64,`, "BROWSER http://shop.test/"} {
		if !strings.Contains(h, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
}
