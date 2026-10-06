package scenario

import "fmt"

// Overrides change a scenario's load at run time without editing the file,
// for example "same journeys, spike shape".
type Overrides struct {
	Shape      string
	Mode       string
	VUs        int
	Rate       string
	Duration   string
	Start      string
	Max        string
	Iterations int
}

// Apply rewrites the load section. A shape replaces explicit stages; a
// duration or rate without a shape switches to a constant executor.
func (o Overrides) Apply(s *Scenario) error {
	l := &s.Load
	if l.Mode == ModeReplay && (o.Shape != "" || o.Mode != "" || o.VUs != 0 || o.Rate != "" || o.Duration != "" || o.Iterations != 0) {
		return fmt.Errorf("a replay sends the recording as recorded; change load.replay.speed instead of the load")
	}
	if o.Shape != "" {
		l.Shape, l.Stages, l.Iterations = o.Shape, nil, 0
	}
	if o.Mode != "" {
		if o.Mode != ModeVUs && o.Mode != ModeRate {
			return fmt.Errorf("mode must be vus or rate")
		}
		l.Mode = o.Mode
	}
	if o.Rate != "" {
		r, err := ParseRate(o.Rate)
		if err != nil {
			return err
		}
		l.Mode, l.Rate = ModeRate, r
		if o.Shape == "" && l.Shape == "" {
			l.Stages, l.Iterations = nil, 0
		}
	}
	if o.VUs > 0 {
		l.VUs = o.VUs
		if o.Rate == "" && o.Mode == "" {
			l.Mode = ModeVUs
		}
	}
	if o.Duration != "" {
		d, err := ParseDuration(o.Duration)
		if err != nil {
			return err
		}
		l.Duration = d
		if o.Shape == "" {
			l.Stages, l.Iterations = nil, 0
		}
	}
	if o.Start != "" {
		l.Start = o.Start
	}
	if o.Max != "" {
		l.Max = o.Max
	}
	if o.Iterations > 0 {
		l.Iterations, l.Shape, l.Stages, l.Mode, l.Duration = o.Iterations, "", nil, ModeVUs, 0
	}
	return nil
}
