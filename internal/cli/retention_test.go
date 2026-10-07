package cli

import (
	"testing"
	"time"
)

func TestParseRetention(t *testing.T) {
	for in, want := range map[string]time.Duration{"": 0, "0": 0, "30d": 30 * 24 * time.Hour, "36h": 36 * time.Hour} {
		if got, err := parseRetention(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"1h", "abc", "-5d"} {
		if _, err := parseRetention(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
