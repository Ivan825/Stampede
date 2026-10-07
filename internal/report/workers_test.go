package report

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestTimeBands(t *testing.T) {
	r := &Report{
		Faults: []FaultEvent{
			{Label: "latency 200ms", Start: 5, End: 10},
			{Label: "not applied", Start: 1, End: 2, Error: "agent unreachable"},
		},
		Workers: []WorkerRow{
			{Name: "w1", Saturated: []Span{{From: 3, To: 6}, {From: 8, To: 9}}, SaturationReasons: []string{"cpu", "sched-lag"}},
			{Name: "w2", Lost: &Span{From: 7, To: 12}},
			{Name: "w3"},
		},
	}
	got := r.timeBands()
	want := []band{
		{From: 5, To: 10, Label: "latency 200ms", Kind: bandFault},
		{From: 3, To: 6, Label: "w1 saturated (cpu, sched-lag)", Kind: bandSaturated},
		{From: 8, To: 9, Label: "w1 saturated (cpu, sched-lag)", Kind: bandSaturated},
		{From: 7, To: 12, Label: "w2 lost", Kind: bandLost},
	}
	if len(got) != len(want) {
		t.Fatalf("bands = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("band %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestHTMLShadesSaturatedAndLostWindows(t *testing.T) {
	r := build(t, 20*time.Millisecond, 0)
	r.Workers = []WorkerRow{
		{ID: "a", Name: "gen-1", Region: "eu-west-1", ShareLo: 0, ShareHi: 0.5, State: "finished", PeakVUs: 1, Requests: 300,
			Saturated: []Span{{From: 1, To: 2}}, SaturationReasons: []string{"cpu"}, ClockOffset: 0.0012},
		{ID: "b", Name: "gen-2", ShareLo: 0.5, ShareHi: 1, State: "lost", PeakVUs: 1, Requests: 300, Lost: &Span{From: 2, To: 3}},
	}
	var buf bytes.Buffer
	if err := r.WriteHTML(&buf); err != nil {
		t.Fatal(err)
	}
	h := buf.String()
	for _, want := range []string{
		`<rect class="band saturated"`,
		`<title>gen-1 saturated (cpu)</title>`,
		`<rect class="band lost"`,
		`<title>gen-2 lost</title>`,
		`<i class="swatch band saturated"></i>worker saturated`,
		`<i class="swatch band lost"></i>worker lost`,
		"<h2>Workers</h2>",
		"eu-west-1",
		"50.0%",
		"1s–2s",
		"1.2ms",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	if strings.Contains(h, "injected fault") {
		t.Error("legend lists injected faults although there are none")
	}

	// Without workers there is no table and no band legend.
	r.Workers = nil
	buf.Reset()
	if err := r.WriteHTML(&buf); err != nil {
		t.Fatal(err)
	}
	if h := buf.String(); strings.Contains(h, "<h2>Workers</h2>") || strings.Contains(h, "swatch band") {
		t.Error("workers table or band legend shown without workers")
	}
}
