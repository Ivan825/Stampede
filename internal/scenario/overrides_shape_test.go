package scenario

import (
	"testing"
	"time"
)

// --shape smoke runs a couple of users (or 1/s) for a minute, not the
// rate and duration the scenario was written for.
func TestOverrideSmokeIgnoresScenarioLoad(t *testing.T) {
	cases := []struct {
		name  string
		load  Load
		ov    Overrides
		peak  float64
		total time.Duration
	}{
		{"rate scenario", Load{Mode: ModeRate, Rate: 20, Duration: Duration(10 * time.Minute)}, Overrides{Shape: "smoke"}, 1, time.Minute},
		{"vus scenario", Load{Mode: ModeVUs, VUs: 50, Duration: Duration(10 * time.Minute)}, Overrides{Shape: "smoke"}, 2, time.Minute},
		{"start and max", Load{Mode: ModeRate, Start: "20/s", Max: "200/s", Shape: "spike"}, Overrides{Shape: "smoke"}, 1, time.Minute},
		{"explicit rate", Load{Mode: ModeRate, Rate: 20}, Overrides{Shape: "smoke", Rate: "5/s"}, 5, time.Minute},
		{"explicit vus", Load{Mode: ModeVUs, VUs: 50}, Overrides{Shape: "smoke", VUs: 3}, 3, time.Minute},
		{"explicit duration", Load{Mode: ModeRate, Rate: 20, Duration: Duration(10 * time.Minute)}, Overrides{Shape: "smoke", Duration: "30s"}, 1, 30 * time.Second},
	}
	for _, c := range cases {
		s := &Scenario{Load: c.load}
		if err := c.ov.Apply(s); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		p, err := s.Load.Plan()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if p.Peak() != c.peak || p.TotalDuration() != c.total {
			t.Errorf("%s: peak %v over %s, want %v over %s", c.name, p.Peak(), p.TotalDuration(), c.peak, c.total)
		}
	}
}

// A scenario file that sets shape: smoke keeps its own level.
func TestSmokeShapeInFileKeepsLevel(t *testing.T) {
	p, err := Load{Mode: ModeRate, Shape: "smoke", Rate: 5}.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if p.Peak() != 5 {
		t.Errorf("peak %v, want 5", p.Peak())
	}
}

// --shape breakpoint --rate 20/s works without a max: the steps go from
// 20/s to ten times that.
func TestOverrideBreakpointFromRate(t *testing.T) {
	s := &Scenario{Load: Load{Mode: ModeVUs, VUs: 10, Duration: Duration(time.Minute)}}
	if err := (Overrides{Shape: "breakpoint", Rate: "20/s"}).Apply(s); err != nil {
		t.Fatal(err)
	}
	p, err := s.Load.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != ModeRate || p.Stages[0].Target != 20 || p.Peak() != 200 || !p.StopOnFail {
		t.Errorf("mode %s, first %v, peak %v, stop %v; want rate, 20, 200, true", p.Mode, p.Stages[0].Target, p.Peak(), p.StopOnFail)
	}

	s = &Scenario{Load: Load{Mode: ModeRate, Rate: 20}}
	if err := (Overrides{Shape: "breakpoint", Start: "20/s", Max: "400/s"}).Apply(s); err != nil {
		t.Fatal(err)
	}
	if p, err = s.Load.Plan(); err != nil || p.Peak() != 400 {
		t.Errorf("--start/--max: peak %v, err %v; want 400", p.Peak(), err)
	}

	if _, err := (Load{Mode: ModeRate, Shape: "breakpoint"}).Plan(); err == nil {
		t.Error("breakpoint with no level at all should be refused")
	}
}
