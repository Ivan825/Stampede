// Package cron parses standard five-field cron expressions and works out
// when they next fire, in UTC or an IANA time zone.
//
// Fields are minute, hour, day of month, month and day of week. Each takes
// *, a number, a range (1-5), a list (1,3,5) and a step (*/15 or 10-50/10).
// Months and weekdays also take three-letter names (JAN, MON), and 7 is
// Sunday as well as 0. The macros @yearly, @annually, @monthly, @weekly,
// @daily, @midnight and @hourly are accepted.
//
// As in Vixie cron, when both day of month and day of week are restricted
// (neither starts with *), a day matches if either does.
//
// Daylight saving: each wall-clock time fires at most once. A time that
// does not exist because the clocks went forward fires when it would have
// been, shifted by the jump (02:30 becomes 03:30 when 02:00 jumps to
// 03:00). A time that happens twice because the clocks went back fires
// the first time only.
package cron

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed cron expression bound to a time zone.
type Schedule struct {
	minute, hour, dom, month, dow uint64 // bit sets
	domStar, dowStar              bool
	loc                           *time.Location
	expr                          string
}

type field struct {
	name     string
	min, max int
	names    map[string]int
}

var (
	minuteField = field{name: "minute", min: 0, max: 59}
	hourField   = field{name: "hour", min: 0, max: 23}
	domField    = field{name: "day of month", min: 1, max: 31}
	monthField  = field{name: "month", min: 1, max: 12, names: map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	}}
	// Day of week allows 7 for Sunday; it is folded onto 0 after parsing.
	dowField = field{name: "day of week", min: 0, max: 7, names: map[string]int{
		"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	}}
)

var macros = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

// Parse reads a five-field expression. tz is an IANA zone name such as
// Europe/London; empty means UTC.
func Parse(expr, tz string) (*Schedule, error) {
	loc := time.UTC
	if tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return nil, fmt.Errorf("unknown time zone %q (use an IANA name such as Europe/London)", tz)
		}
		loc = l
	}
	expr = strings.TrimSpace(expr)
	spec := expr
	if strings.HasPrefix(spec, "@") {
		m, ok := macros[strings.ToLower(spec)]
		if !ok {
			return nil, fmt.Errorf("unknown macro %q", spec)
		}
		spec = m
	}
	parts := strings.Fields(spec)
	if len(parts) != 5 {
		return nil, fmt.Errorf("a cron expression has 5 fields (minute hour day-of-month month day-of-week), got %d", len(parts))
	}
	s := &Schedule{loc: loc, expr: expr}
	var err error
	if s.minute, err = parseField(parts[0], minuteField); err != nil {
		return nil, err
	}
	if s.hour, err = parseField(parts[1], hourField); err != nil {
		return nil, err
	}
	if s.dom, err = parseField(parts[2], domField); err != nil {
		return nil, err
	}
	if s.month, err = parseField(parts[3], monthField); err != nil {
		return nil, err
	}
	if s.dow, err = parseField(parts[4], dowField); err != nil {
		return nil, err
	}
	if s.dow&(1<<7) != 0 {
		s.dow = s.dow&^(1<<7) | 1
	}
	s.domStar = strings.HasPrefix(parts[2], "*")
	s.dowStar = strings.HasPrefix(parts[4], "*")
	if !s.possible() {
		return nil, errors.New("the expression never fires (no month has that day)")
	}
	return s, nil
}

// possible reports whether some month has a matching day of month. When
// the day of week is restricted, a matching weekday always exists, so this
// matters only when it is not.
func (s *Schedule) possible() bool {
	if !s.dowStar {
		return true
	}
	maxDays := [13]int{0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	for m := 1; m <= 12; m++ {
		if s.month&(1<<m) == 0 {
			continue
		}
		for d := 1; d <= maxDays[m]; d++ {
			if s.dom&(1<<d) != 0 {
				return true
			}
		}
	}
	return false
}

func parseField(text string, f field) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(text, ",") {
		b, err := parsePart(part, f)
		if err != nil {
			return 0, err
		}
		bits |= b
	}
	return bits, nil
}

func parsePart(part string, f field) (uint64, error) {
	if part == "" {
		return 0, fmt.Errorf("%s: empty list item", f.name)
	}
	rng, stepText, hasStep := strings.Cut(part, "/")
	step := 1
	if hasStep {
		n, err := strconv.Atoi(stepText)
		if err != nil || n < 1 {
			return 0, fmt.Errorf("%s: step %q must be a positive number", f.name, stepText)
		}
		step = n
	}
	lo, hi := f.min, f.max
	if f.name == dowField.name {
		hi = 6 // * means Sunday to Saturday once
	}
	switch {
	case rng == "*":
	case strings.Contains(rng, "-"):
		a, b, _ := strings.Cut(rng, "-")
		var err error
		if lo, err = value(a, f); err != nil {
			return 0, err
		}
		if hi, err = value(b, f); err != nil {
			return 0, err
		}
		if lo > hi {
			return 0, fmt.Errorf("%s: range %s goes backwards", f.name, rng)
		}
	default:
		v, err := value(rng, f)
		if err != nil {
			return 0, err
		}
		lo = v
		if hasStep {
			hi = f.max // 5/15 means 5, 20, 35, 50
		} else {
			hi = v
		}
	}
	if hasStep && step > f.max-f.min+1 {
		return 0, fmt.Errorf("%s: step %d is larger than the field", f.name, step)
	}
	var bits uint64
	for v := lo; v <= hi; v += step {
		bits |= 1 << v
	}
	return bits, nil
}

func value(s string, f field) (int, error) {
	if v, ok := f.names[strings.ToLower(s)]; ok {
		return v, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a number", f.name, s)
	}
	if n < f.min || n > f.max {
		return 0, fmt.Errorf("%s: %d is out of range %d-%d", f.name, n, f.min, f.max)
	}
	return n, nil
}

// String returns the expression as given.
func (s *Schedule) String() string { return s.expr }

// Location is the schedule's time zone.
func (s *Schedule) Location() *time.Location { return s.loc }

func (s *Schedule) dayMatches(t time.Time) bool {
	if s.month&(1<<int(t.Month())) == 0 {
		return false
	}
	dom := s.dom&(1<<t.Day()) != 0
	dow := s.dow&(1<<int(t.Weekday())) != 0
	if s.domStar || s.dowStar {
		return dom && dow
	}
	return dom || dow
}

// searchYears bounds the search; 29 February can be eight years away.
const searchYears = 9

// Next returns the first firing strictly after t, or the zero time if
// there is none within nine years.
func (s *Schedule) Next(after time.Time) time.Time {
	after = after.In(s.loc)
	day := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, time.UTC)
	end := day.AddDate(searchYears, 0, 0)
	for ; !day.After(end); day = day.AddDate(0, 0, 1) {
		if !s.dayMatches(day) {
			continue
		}
		// Normalising a time in a spring-forward gap can reorder times
		// within the day, so take the earliest candidate after t.
		var best time.Time
		for h := 0; h < 24; h++ {
			if s.hour&(1<<h) == 0 {
				continue
			}
			for m := 0; m < 60; m++ {
				if s.minute&(1<<m) == 0 {
					continue
				}
				c := s.wall(day.Year(), day.Month(), day.Day(), h, m)
				if c.After(after) && (best.IsZero() || c.Before(best)) {
					best = c
				}
			}
		}
		if !best.IsZero() {
			return best
		}
	}
	return time.Time{}
}

// wall returns the instant for a wall-clock time in the schedule's zone.
// When the time happens twice (clocks went back) it is the first one; when
// it does not exist (clocks went forward) it is shifted by the jump.
func (s *Schedule) wall(y int, mo time.Month, d, h, mi int) time.Time {
	naive := time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
	t := time.Date(y, mo, d, h, mi, 0, 0, s.loc)
	var offs []int
	for _, probe := range []time.Duration{0, -3 * time.Hour, 3 * time.Hour} {
		_, off := t.Add(probe).Zone()
		offs = append(offs, off)
	}
	var best time.Time
	for _, off := range offs {
		c := naive.Add(-time.Duration(off) * time.Second).In(s.loc)
		if ch, cm, _ := c.Clock(); ch == h && cm == mi && c.Day() == d && (best.IsZero() || c.Before(best)) {
			best = c
		}
	}
	if !best.IsZero() {
		return best
	}
	// In a gap: read the time with the offset from before the jump, which
	// is the smaller one, so it lands after the jump by the same amount.
	return naive.Add(-time.Duration(slices.Min(offs)) * time.Second).In(s.loc)
}

// NextN returns the next n firings after t.
func (s *Schedule) NextN(after time.Time, n int) []time.Time {
	out := make([]time.Time, 0, n)
	for len(out) < n {
		after = s.Next(after)
		if after.IsZero() {
			break
		}
		out = append(out, after)
	}
	return out
}
