package scenario

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Duration is a time.Duration that reads "500ms", "2s", "1m30s", "4h" or
// "2d" from YAML/JSON, and treats a bare number as seconds.
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

// ParseDuration parses a Stampede duration string.
func ParseDuration(s string) (Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return Duration(f * float64(time.Second)), nil
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return Duration(n * 24 * float64(time.Hour)), nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (use forms like 500ms, 2s, 5m, 1h)", s)
	}
	if v < 0 {
		return 0, fmt.Errorf("negative duration %q", s)
	}
	return Duration(v), nil
}

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := ParseDuration(n.Value)
	if err != nil {
		return nodeErr(n, err)
	}
	*d = v
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

func (d *Duration) UnmarshalJSON(b []byte) error {
	s, err := jsonScalar(b)
	if err != nil {
		return err
	}
	v, err := ParseDuration(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// Rate is a number of events per second. It reads "50/s", "3000/m",
// "100/h" or a bare number (per second).
type Rate float64

// PerSecond returns the rate in events per second.
func (r Rate) PerSecond() float64 { return float64(r) }

func (r Rate) String() string {
	return strconv.FormatFloat(float64(r), 'f', -1, 64) + "/s"
}

// ParseRate parses a Stampede rate string.
func ParseRate(s string) (Rate, error) {
	s = strings.TrimSpace(s)
	num, unit, hasUnit := strings.Cut(s, "/")
	f, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("invalid rate %q (use forms like 50/s, 3000/m)", s)
	}
	if !hasUnit {
		return Rate(f), nil
	}
	switch strings.TrimSpace(unit) {
	case "s", "sec", "second":
		return Rate(f), nil
	case "m", "min", "minute":
		return Rate(f / 60), nil
	case "h", "hour":
		return Rate(f / 3600), nil
	default:
		return 0, fmt.Errorf("invalid rate unit in %q (use /s, /m or /h)", s)
	}
}

func (r *Rate) UnmarshalYAML(n *yaml.Node) error {
	v, err := ParseRate(n.Value)
	if err != nil {
		return nodeErr(n, err)
	}
	*r = v
	return nil
}

func (r Rate) MarshalYAML() (any, error) { return r.String(), nil }

func (r *Rate) UnmarshalJSON(b []byte) error {
	s, err := jsonScalar(b)
	if err != nil {
		return err
	}
	v, err := ParseRate(s)
	if err != nil {
		return err
	}
	*r = v
	return nil
}

func (r Rate) MarshalJSON() ([]byte, error) { return json.Marshal(r.String()) }

// Percent is a ratio stored as a fraction (1% = 0.01). It reads "1%",
// "0.5%" or a bare fraction such as 0.01.
type Percent float64

func (p Percent) String() string {
	return strconv.FormatFloat(float64(p)*100, 'f', -1, 64) + "%"
}

// ParsePercent parses "1%" or a fraction.
func ParsePercent(s string) (Percent, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "%") {
		f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, "%")), 64)
		if err != nil || f < 0 || f > 100 {
			return 0, fmt.Errorf("invalid percentage %q", s)
		}
		return Percent(f / 100), nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 1 {
		return 0, fmt.Errorf("invalid percentage %q (use forms like 1%% or 0.01)", s)
	}
	return Percent(f), nil
}

func (p *Percent) UnmarshalYAML(n *yaml.Node) error {
	v, err := ParsePercent(n.Value)
	if err != nil {
		return nodeErr(n, err)
	}
	*p = v
	return nil
}

func (p Percent) MarshalYAML() (any, error) { return p.String(), nil }

func (p *Percent) UnmarshalJSON(b []byte) error {
	s, err := jsonScalar(b)
	if err != nil {
		return err
	}
	v, err := ParsePercent(s)
	if err != nil {
		return err
	}
	*p = v
	return nil
}

func (p Percent) MarshalJSON() ([]byte, error) { return json.Marshal(p.String()) }

// ThinkTime is a pause, either fixed ("2s") or uniformly random within a
// range ("2s..6s").
type ThinkTime struct {
	Min Duration
	Max Duration
}

// ParseThinkTime parses "2s" or "2s..6s".
func ParseThinkTime(s string) (ThinkTime, error) {
	lo, hi, isRange := strings.Cut(strings.TrimSpace(s), "..")
	min, err := ParseDuration(lo)
	if err != nil {
		return ThinkTime{}, err
	}
	if !isRange {
		return ThinkTime{Min: min, Max: min}, nil
	}
	max, err := ParseDuration(hi)
	if err != nil {
		return ThinkTime{}, err
	}
	if max < min {
		return ThinkTime{}, fmt.Errorf("think time range %q has max below min", s)
	}
	return ThinkTime{Min: min, Max: max}, nil
}

func (t ThinkTime) String() string {
	if t.Min == t.Max {
		return t.Min.String()
	}
	return t.Min.String() + ".." + t.Max.String()
}

func (t *ThinkTime) UnmarshalYAML(n *yaml.Node) error {
	v, err := ParseThinkTime(n.Value)
	if err != nil {
		return nodeErr(n, err)
	}
	*t = v
	return nil
}

func (t ThinkTime) MarshalYAML() (any, error) { return t.String(), nil }

func (t *ThinkTime) UnmarshalJSON(b []byte) error {
	s, err := jsonScalar(b)
	if err != nil {
		return err
	}
	v, err := ParseThinkTime(s)
	if err != nil {
		return err
	}
	*t = v
	return nil
}

func (t ThinkTime) MarshalJSON() ([]byte, error) { return json.Marshal(t.String()) }

// jsonScalar accepts a JSON string or number and returns its text.
func jsonScalar(b []byte) (string, error) {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		return s, nil
	}
	var f json.Number
	if err := json.Unmarshal(b, &f); err != nil {
		return "", fmt.Errorf("expected a string or number, got %s", string(b))
	}
	return f.String(), nil
}

func nodeErr(n *yaml.Node, err error) error {
	return fmt.Errorf("line %d: %w", n.Line, err)
}
