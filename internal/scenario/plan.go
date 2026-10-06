package scenario

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Executor names.
const (
	ExecConstantVUs  = "constant-vus"
	ExecRampingVUs   = "ramping-vus"
	ExecConstantRate = "constant-rate"
	ExecRampingRate  = "ramping-rate"
	ExecIterations   = "iterations"
)

// Shapes lists the traffic shape presets.
var Shapes = []string{"smoke", "baseline", "stress", "spike", "soak", "breakpoint", "steps", "recovery", "wave"}

// Plan is a load section resolved into concrete executor settings.
type Plan struct {
	Shape    string
	Mode     string
	Executor string
	// Start is the value at time zero; each stage ramps linearly from the
	// previous value to its Target. Values are users or rates per second.
	Start  float64
	Stages []PlanStage
	// Constant executors use Value for Duration.
	Value      float64
	Duration   time.Duration
	Iterations int
	VUs        int
	// Rates is the planned rate in each second of a replay plan.
	Rates []float64
	// MaxVUs caps the pool in rate mode.
	MaxVUs       int
	GracefulStop time.Duration
	// StopOnFail ends the run once a threshold fails (breakpoint shape).
	StopOnFail bool
}

// PlanStage ramps linearly to Target over Duration.
type PlanStage struct {
	Duration time.Duration
	Target   float64
}

// TotalDuration is the planned length of the load phase, excluding the
// graceful stop.
func (p *Plan) TotalDuration() time.Duration {
	if len(p.Stages) == 0 {
		return p.Duration
	}
	var d time.Duration
	for _, s := range p.Stages {
		d += s.Duration
	}
	return d
}

// Peak is the highest planned value (users or rate).
func (p *Plan) Peak() float64 {
	m := math.Max(p.Start, p.Value)
	for _, s := range p.Stages {
		m = math.Max(m, s.Target)
	}
	return m
}

// ValueAt returns the planned users or rate at elapsed time t.
func (p *Plan) ValueAt(t time.Duration) float64 {
	if p.Rates != nil {
		if i := int(t / time.Second); i >= 0 && i < len(p.Rates) {
			return p.Rates[i]
		}
		return 0
	}
	if len(p.Stages) == 0 {
		return p.Value
	}
	prev := p.Start
	var at time.Duration
	for _, s := range p.Stages {
		if t < at+s.Duration {
			frac := float64(t-at) / float64(s.Duration)
			return prev + (s.Target-prev)*frac
		}
		at += s.Duration
		prev = s.Target
	}
	return prev
}

// Plan resolves the load section into executor settings.
func (l Load) Plan() (*Plan, error) {
	if l.Mode == ModeReplay {
		return l.replayPlan()
	}
	p := &Plan{Shape: l.Shape, Mode: l.Mode, GracefulStop: l.GracefulStop.D(), MaxVUs: l.MaxVUs}
	if p.Mode != ModeVUs && p.Mode != ModeRate {
		return nil, fmt.Errorf("mode must be vus or rate, got %q", l.Mode)
	}
	parse := func(field, s string) (float64, error) {
		if s == "" {
			return 0, nil
		}
		if p.Mode == ModeRate {
			r, err := ParseRate(s)
			return float64(r), err
		}
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%s: %q is not a user count (mode is vus)", field, s)
		}
		return float64(n), nil
	}

	switch {
	case len(l.Stages) > 0:
		for i, st := range l.Stages {
			v, err := parse(fmt.Sprintf("stages[%d].target", i), st.Target)
			if err != nil {
				return nil, err
			}
			if st.Duration <= 0 {
				return nil, fmt.Errorf("stages[%d].duration must be positive", i)
			}
			p.Stages = append(p.Stages, PlanStage{Duration: st.Duration.D(), Target: v})
		}
		start, err := parse("start", l.Start)
		if err != nil {
			return nil, err
		}
		p.Start = start
	case l.Shape != "":
		if err := l.shapeStages(p, parse); err != nil {
			return nil, err
		}
	case l.Iterations > 0:
		if p.Mode == ModeRate {
			return nil, fmt.Errorf("iterations runs in vus mode")
		}
		p.Executor = ExecIterations
		p.Iterations = l.Iterations
		p.VUs = max(l.VUs, 1)
		if p.VUs > p.Iterations {
			p.VUs = p.Iterations
		}
		p.Duration = l.Duration.D() // optional cap
		return p, nil
	default:
		if l.Duration <= 0 {
			return nil, fmt.Errorf("set duration, stages, iterations or a shape")
		}
		p.Duration = l.Duration.D()
		if p.Mode == ModeRate {
			if l.Rate <= 0 {
				return nil, fmt.Errorf("rate mode needs rate, for example rate: 50/s")
			}
			p.Executor, p.Value = ExecConstantRate, float64(l.Rate)
		} else {
			if l.VUs <= 0 {
				return nil, fmt.Errorf("vus mode needs vus, for example vus: 10")
			}
			p.Executor, p.Value, p.VUs = ExecConstantVUs, float64(l.VUs), l.VUs
		}
	}

	if p.Executor == "" {
		if p.Mode == ModeRate {
			p.Executor = ExecRampingRate
		} else {
			p.Executor = ExecRampingVUs
		}
	}
	if p.Mode == ModeRate && p.MaxVUs == 0 {
		p.MaxVUs = int(math.Min(50000, math.Max(50, math.Ceil(p.Peak()*5))))
	}
	if p.Mode == ModeVUs {
		p.VUs = int(math.Ceil(p.Peak()))
	}
	if p.TotalDuration() <= 0 {
		return nil, fmt.Errorf("planned duration is zero")
	}
	if p.Peak() <= 0 {
		return nil, fmt.Errorf("planned load is zero; set vus, rate, start/max or stage targets")
	}
	return p, nil
}

func (l Load) shapeStages(p *Plan, parse func(string, string) (float64, error)) error {
	start, err := parse("start", l.Start)
	if err != nil {
		return err
	}
	peak, err := parse("max", l.Max)
	if err != nil {
		return err
	}
	// vus/rate act as the expected level when start is not given.
	if start == 0 {
		if p.Mode == ModeRate {
			start = float64(l.Rate)
		} else {
			start = float64(l.VUs)
		}
	}
	m := time.Minute
	st := func(d time.Duration, v float64) PlanStage { return PlanStage{Duration: d, Target: v} }
	scale := func(stages []PlanStage) []PlanStage {
		if l.Duration <= 0 {
			return stages
		}
		var total time.Duration
		for _, s := range stages {
			total += s.Duration
		}
		f := float64(l.Duration) / float64(total)
		var sum time.Duration
		for i := range stages {
			stages[i].Duration = time.Duration(float64(stages[i].Duration) * f).Round(time.Millisecond)
			if stages[i].Duration < time.Second {
				stages[i].Duration = time.Second
			}
			sum += stages[i].Duration
		}
		// Absorb rounding in the last stage so the total is exact.
		if last := &stages[len(stages)-1]; last.Duration+l.Duration.D()-sum >= time.Second {
			last.Duration += l.Duration.D() - sum
		}
		return stages
	}
	need := func(what string, v float64) error {
		if v <= 0 {
			return fmt.Errorf("shape %s needs %s", l.Shape, what)
		}
		return nil
	}

	switch l.Shape {
	case "smoke":
		v := start
		if v == 0 {
			v = 2
			if p.Mode == ModeRate {
				v = 1
			}
		}
		d := l.Duration.D()
		if d == 0 {
			d = time.Minute
		}
		p.Stages = []PlanStage{st(d, v)}
		p.Start = v
	case "baseline":
		if err := need("start (the expected normal load)", start); err != nil {
			return err
		}
		hold := l.Duration.D()
		if hold == 0 {
			hold = 10 * m
		}
		p.Stages = []PlanStage{st(m, start), st(hold, start)}
	case "stress":
		if err := need("start (the expected peak)", start); err != nil {
			return err
		}
		if peak == 0 {
			peak = start * 2
		}
		p.Stages = scale([]PlanStage{st(2*m, start), st(5*m, start), st(5*m, peak), st(5*m, peak), st(2*m, 0)})
	case "spike":
		if err := need("start (normal load) and max (spike height)", start); err != nil {
			return err
		}
		if peak == 0 {
			peak = start * 10
		}
		p.Stages = scale([]PlanStage{st(m, start), st(2*m, start), st(10*time.Second, peak), st(3*m, peak), st(10*time.Second, start), st(3*m, start)})
	case "soak":
		if err := need("start (the sustained load)", start); err != nil {
			return err
		}
		hold := l.Duration.D()
		if hold == 0 {
			hold = 4 * time.Hour
		}
		p.Stages = []PlanStage{st(2*m, start), st(hold, start), st(2*m, 0)}
	case "breakpoint", "steps":
		if err := need("max (the highest load to try)", peak); err != nil {
			return err
		}
		n := l.Steps
		if n == 0 {
			n = 10
			if l.Shape == "steps" {
				n = 5
			}
		}
		sd := l.StepDuration.D()
		if sd == 0 {
			sd = 2 * m
		}
		ramp := sd / 6
		if ramp < time.Second {
			ramp = time.Second
		}
		lo := start
		if lo == 0 {
			lo = peak / float64(n)
		}
		p.Start = 0
		for i := 0; i < n; i++ {
			v := lo
			if n > 1 {
				v = lo + (peak-lo)*float64(i)/float64(n-1)
			}
			p.Stages = append(p.Stages, st(ramp, v), st(sd-ramp, v))
		}
		p.StopOnFail = l.Shape == "breakpoint"
	case "recovery":
		if err := need("start (normal load)", start); err != nil {
			return err
		}
		if peak == 0 {
			peak = start * 3
		}
		p.Stages = scale([]PlanStage{st(m, start), st(2*m, start), st(30*time.Second, peak), st(3*m, peak), st(30*time.Second, start), st(5*m, start)})
	case "wave":
		if err := need("start (trough) and max (crest)", start); err != nil {
			return err
		}
		if peak == 0 {
			peak = start * 3
		}
		cycles := l.Cycles
		if cycles == 0 {
			cycles = 4
		}
		period := 5 * m
		if l.Duration > 0 {
			period = l.Duration.D() / time.Duration(cycles)
		}
		q := period / 4
		p.Stages = append(p.Stages, st(m, start))
		for i := 0; i < cycles; i++ {
			p.Stages = append(p.Stages, st(q, peak), st(q, peak), st(q, start), st(q, start))
		}
	default:
		return fmt.Errorf("unknown shape %q (use %s)", l.Shape, strings.Join(Shapes, ", "))
	}
	return nil
}
