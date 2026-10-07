package scenario

import "testing"

func TestOverrideMaxVUs(t *testing.T) {
	s := &Scenario{Load: Load{Mode: ModeRate, Rate: 100, Duration: Duration(60e9)}}
	if err := (Overrides{MaxVUs: 3000}).Apply(s); err != nil {
		t.Fatal(err)
	}
	p, err := s.Load.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if p.MaxVUs != 3000 {
		t.Errorf("MaxVUs %d, want 3000", p.MaxVUs)
	}
}
