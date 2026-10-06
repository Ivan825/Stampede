package cron

import (
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, expr, tz string) *Schedule {
	t.Helper()
	s, err := Parse(expr, tz)
	if err != nil {
		t.Fatalf("Parse(%q, %q): %v", expr, tz, err)
	}
	return s
}

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNextUTC(t *testing.T) {
	cases := []struct {
		expr, after, want string
	}{
		{"* * * * *", "2026-10-07T10:15:30Z", "2026-10-07T10:16:00Z"},
		{"* * * * *", "2026-10-07T10:15:00Z", "2026-10-07T10:16:00Z"}, // strictly after
		{"*/15 * * * *", "2026-10-07T10:15:00Z", "2026-10-07T10:30:00Z"},
		{"5/20 * * * *", "2026-10-07T10:26:00Z", "2026-10-07T10:45:00Z"},
		{"0 2 * * *", "2026-10-07T02:00:00Z", "2026-10-08T02:00:00Z"},
		{"30 9 * * MON-FRI", "2026-10-09T10:00:00Z", "2026-10-12T09:30:00Z"}, // Friday after → Monday
		{"0 0 * * 7", "2026-10-07T00:00:00Z", "2026-10-11T00:00:00Z"},        // 7 is Sunday
		{"0 0 * * sun", "2026-10-07T00:00:00Z", "2026-10-11T00:00:00Z"},
		{"0 0 1 */3 *", "2026-10-07T00:00:00Z", "2027-01-01T00:00:00Z"},
		{"0 0 31 * *", "2026-10-31T00:00:00Z", "2026-12-31T00:00:00Z"}, // skips November
		{"0 0 29 2 *", "2026-01-01T00:00:00Z", "2028-02-29T00:00:00Z"},
		{"0 0 29 2 *", "2096-03-01T00:00:00Z", "2104-02-29T00:00:00Z"}, // 2100 is not a leap year
		{"0,30 8-10 * * *", "2026-10-07T10:30:00Z", "2026-10-08T08:00:00Z"},
		{"@hourly", "2026-10-07T10:15:00Z", "2026-10-07T11:00:00Z"},
		{"@daily", "2026-10-07T10:15:00Z", "2026-10-08T00:00:00Z"},
		{"@weekly", "2026-10-07T10:15:00Z", "2026-10-11T00:00:00Z"},
		{"@monthly", "2026-10-07T10:15:00Z", "2026-11-01T00:00:00Z"},
		{"@yearly", "2026-10-07T10:15:00Z", "2027-01-01T00:00:00Z"},
		{"0 12 1-5 * MON", "2026-10-07T00:00:00Z", "2026-10-12T12:00:00Z"}, // either: Monday the 12th
		{"0 12 13 * FRI", "2026-10-07T00:00:00Z", "2026-10-09T12:00:00Z"},  // either: Friday 9th first
		{"0 12 13 * */2", "2026-10-14T00:00:00Z", "2026-12-13T12:00:00Z"},  // * prefix means both: a 13th on Sun/Tue/Thu/Sat
	}
	for _, c := range cases {
		got := mustParse(t, c.expr, "").Next(utc(c.after))
		if !got.Equal(utc(c.want)) {
			t.Errorf("%q after %s: got %s, want %s", c.expr, c.after, got.UTC().Format(time.RFC3339), c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"":                "5 fields",
		"* * * *":         "5 fields",
		"* * * * * *":     "5 fields",
		"60 * * * *":      "out of range",
		"* 24 * * *":      "out of range",
		"* * 0 * *":       "out of range",
		"* * * 13 *":      "out of range",
		"* * * * 8":       "out of range",
		"5-1 * * * *":     "backwards",
		"*/0 * * * *":     "positive",
		"*/x * * * *":     "positive",
		"*/61 * * * *":    "larger than",
		"a * * * *":       "not a number",
		"1,,2 * * * *":    "empty",
		"@every 5m":       "unknown macro",
		"0 0 30 2 *":      "never fires",
		"0 0 31 4,6,9 *":  "never fires",
		"* * * FOO *":     "not a number",
		"* * * * MON-FOO": "not a number",
	}
	for expr, want := range cases {
		_, err := Parse(expr, "")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q): got %v, want error containing %q", expr, err, want)
		}
	}
	if _, err := Parse("* * * * *", "Mars/Olympus_Mons"); err == nil || !strings.Contains(err.Error(), "time zone") {
		t.Errorf("bad zone: %v", err)
	}
	// Restricting the weekday makes the 30th of February possible (either rule).
	if _, err := Parse("0 0 30 2 MON", ""); err != nil {
		t.Errorf("either rule: %v", err)
	}
}

func TestTimeZone(t *testing.T) {
	s := mustParse(t, "0 9 * * *", "Asia/Kolkata")
	got := s.Next(utc("2026-10-07T00:00:00Z"))
	if !got.Equal(utc("2026-10-07T03:30:00Z")) {
		t.Errorf("09:00 IST: %s", got.UTC())
	}
	if s.Location().String() != "Asia/Kolkata" || s.String() != "0 9 * * *" {
		t.Errorf("accessors: %s %s", s.Location(), s)
	}
}

func TestDaylightSavingSpringForward(t *testing.T) {
	// New York, 8 March 2026: 02:00 EST jumps to 03:00 EDT.
	s := mustParse(t, "30 2 * * *", "America/New_York")
	got := s.NextN(utc("2026-03-07T00:00:00Z"), 3)
	want := []string{
		"2026-03-07T07:30:00Z", // 02:30 EST
		"2026-03-08T07:30:00Z", // 02:30 does not exist: 03:30 EDT
		"2026-03-09T06:30:00Z", // 02:30 EDT
	}
	for i := range want {
		if !got[i].Equal(utc(want[i])) {
			t.Errorf("firing %d: got %s, want %s", i, got[i].UTC(), want[i])
		}
	}
	// Hourly across the gap: 01:00 EST, then 03:00 EDT (02:00 is shifted
	// onto 03:00 and fires once), then 04:00 EDT.
	h := mustParse(t, "0 * * * *", "America/New_York")
	got = h.NextN(utc("2026-03-08T05:30:00Z"), 3)
	want = []string{"2026-03-08T06:00:00Z", "2026-03-08T07:00:00Z", "2026-03-08T08:00:00Z"}
	for i := range want {
		if !got[i].Equal(utc(want[i])) {
			t.Errorf("hourly firing %d: got %s, want %s", i, got[i].UTC(), want[i])
		}
	}
}

func TestDaylightSavingFallBack(t *testing.T) {
	// New York, 1 November 2026: 02:00 EDT falls back to 01:00 EST, so
	// 01:30 happens twice. It fires once, the first time.
	s := mustParse(t, "30 1 * * *", "America/New_York")
	got := s.NextN(utc("2026-10-31T00:00:00Z"), 3)
	want := []string{
		"2026-10-31T05:30:00Z", // 01:30 EDT
		"2026-11-01T05:30:00Z", // first 01:30 (EDT)
		"2026-11-02T06:30:00Z", // 01:30 EST
	}
	for i := range want {
		if !got[i].Equal(utc(want[i])) {
			t.Errorf("firing %d: got %s, want %s", i, got[i].UTC(), want[i])
		}
	}
	// Asking from inside the repeated hour does not fire 01:30 again.
	if n := s.Next(utc("2026-11-01T05:45:00Z")); !n.Equal(utc("2026-11-02T06:30:00Z")) {
		t.Errorf("repeat fired: %s", n.UTC())
	}
	// Europe/London, 25 October 2026: 02:00 BST falls back to 01:00 GMT.
	l := mustParse(t, "15 1 * * *", "Europe/London")
	if n := l.Next(utc("2026-10-25T00:00:00Z")); !n.Equal(utc("2026-10-25T00:15:00Z")) {
		t.Errorf("London first 01:15: %s", n.UTC())
	}
}

func TestNextNStopsWhenExhausted(t *testing.T) {
	s := mustParse(t, "0 0 29 2 *", "")
	got := s.NextN(utc("2026-01-01T00:00:00Z"), 3)
	if len(got) != 3 || got[2].Year() != 2036 {
		t.Errorf("leap days: %v", got)
	}
	// 29 February falls on a Sunday in 2032 and next in 2060, outside
	// the nine-year search window.
	r := mustParse(t, "0 0 29 2 */7", "")
	if n := r.Next(utc("2026-01-01T00:00:00Z")); !n.Equal(utc("2032-02-29T00:00:00Z")) {
		t.Errorf("29 Feb Sunday: %s", n)
	}
	if n := r.Next(utc("2033-01-01T00:00:00Z")); !n.IsZero() {
		t.Errorf("beyond the window: %s", n)
	}
}

func TestHalfHourJump(t *testing.T) {
	// Lord Howe Island moves its clocks by 30 minutes: on 4 October 2026
	// 02:00 becomes 02:30, so 02:15 is shifted to 02:45 (+11:00).
	s := mustParse(t, "15 2 * * *", "Australia/Lord_Howe")
	if n := s.Next(utc("2026-10-03T12:00:00Z")); !n.Equal(utc("2026-10-03T15:45:00Z")) {
		t.Errorf("Lord Howe gap: %s", n.UTC())
	}
}
