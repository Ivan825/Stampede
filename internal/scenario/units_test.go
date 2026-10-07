package scenario

import (
	"testing"
	"time"
)

// TestUnitsRejectNonFinite: numbers that parse as floats but cannot be a
// duration, rate or percentage (NaN, infinities, negatives, overflow)
// are errors, not nonsense plans.
func TestUnitsRejectNonFinite(t *testing.T) {
	for _, s := range []string{"NaN", "Inf", "-Inf", "1e300", "-5", "-1d", "NaNd", "1e15d"} {
		if d, err := ParseDuration(s); err == nil {
			t.Errorf("duration %q accepted as %v", s, d)
		}
	}
	for s, want := range map[string]time.Duration{"1.5": 1500 * time.Millisecond, "2d": 48 * time.Hour, "0": 0, "1e9": 1e9 * time.Second} {
		if d, err := ParseDuration(s); err != nil || d.D() != want {
			t.Errorf("duration %q: %v %v", s, d, err)
		}
	}
	for _, s := range []string{"NaN/s", "Inf/s", "+Inf", "NaN"} {
		if r, err := ParseRate(s); err == nil {
			t.Errorf("rate %q accepted as %v", s, r)
		}
	}
	for _, s := range []string{"NaN%", "NaN", "Inf%"} {
		if p, err := ParsePercent(s); err == nil {
			t.Errorf("percentage %q accepted as %v", s, p)
		}
	}
}
