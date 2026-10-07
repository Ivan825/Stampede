package scenario

import (
	"math"
	"os"
	"strings"
	"testing"
)

func TestRegionsParse(t *testing.T) {
	b, err := os.ReadFile("testdata/regions.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Load.RegionFractions()
	want := map[string]float64{"mumbai": 0.5, "frankfurt": 0.3, "virginia": 0.2}
	if len(got) != len(want) {
		t.Fatalf("regions = %v", got)
	}
	for r, f := range want {
		if math.Abs(got[r]-f) > 1e-9 {
			t.Errorf("%s = %v, want %v", r, got[r], f)
		}
	}
	// The split survives a round trip through YAML (servers re-marshal).
	out, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "mumbai: 50%") {
		t.Errorf("marshalled regions:\n%s", out)
	}
	if (&Load{}).RegionFractions() != nil {
		t.Error("no regions should give nil")
	}
}

func TestRegionsValidate(t *testing.T) {
	base := `
metadata: {name: x}
target: {baseURL: http://localhost:8080}
journeys: [{name: a, steps: [{get: /a}]}]
load:
  vus: 1
  duration: 1s
  regions: `
	tests := []struct {
		regions string
		problem string
	}{
		{"{eu: 60%, us: 40%}", ""},
		{"{eu: 1}", ""},
		{"{eu: 60%, us: 30%}", "add up to 100% (they add up to 90%)"},
		{"{eu: 0%, us: 100%}", "load.regions.eu: must be more than 0%"},
		{`{"eu west": 100%}`, "region names are"},
	}
	for _, tc := range tests {
		_, err := Parse([]byte(base + tc.regions))
		switch {
		case tc.problem == "" && err != nil:
			t.Errorf("%s: %v", tc.regions, err)
		case tc.problem != "" && (err == nil || !strings.Contains(err.Error(), tc.problem)):
			t.Errorf("%s: error %v, want %q", tc.regions, err, tc.problem)
		}
	}
}
