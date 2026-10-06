package engine

import (
	"math"
	"runtime"
	"time"

	"github.com/Ivan825/Stampede/internal/scenario"
)

// arrivalSchedule yields the exact intended start time of each arrival for
// a piecewise-linear rate plan. Arrival k happens when the integral of the
// rate from zero reaches k, so there is no drift from tick rounding.
type arrivalSchedule struct {
	stages   []scenario.PlanStage
	start    float64 // rate at time zero
	constant bool
	rate     float64
	total    time.Duration

	k       float64 // next arrival index
	stage   int
	stageT0 time.Duration // start of current stage
	stageA0 float64       // cumulative arrivals at start of current stage
	stageR0 float64       // rate at start of current stage
}

func newArrivalSchedule(p *scenario.Plan) *arrivalSchedule {
	s := &arrivalSchedule{total: p.TotalDuration()}
	if len(p.Stages) == 0 {
		s.constant, s.rate = true, p.Value
		return s
	}
	s.stages, s.start, s.stageR0 = p.Stages, p.Start, p.Start
	return s
}

// next returns the offset from T0 of the next arrival, or false when the
// plan has no more arrivals.
func (s *arrivalSchedule) next() (time.Duration, bool) {
	if s.constant {
		if s.rate <= 0 {
			return 0, false
		}
		at := time.Duration(s.k / s.rate * float64(time.Second))
		if at >= s.total {
			return 0, false
		}
		s.k++
		return at, true
	}
	for s.stage < len(s.stages) {
		st := s.stages[s.stage]
		d := st.Duration.Seconds()
		r0, r1 := s.stageR0, st.Target
		arrivals := (r0 + r1) / 2 * d
		x := s.k - s.stageA0 // arrivals needed within this stage
		if x < arrivals {
			var t float64
			slope := (r1 - r0) / d
			if math.Abs(slope) < 1e-12 {
				t = x / r0
			} else {
				// Solve r0*t + slope*t^2/2 = x for the smallest t >= 0.
				disc := r0*r0 + 2*slope*x
				if disc < 0 {
					disc = 0
				}
				t = (-r0 + math.Sqrt(disc)) / slope
			}
			s.k++
			return s.stageT0 + time.Duration(t*float64(time.Second)), true
		}
		s.stageA0 += arrivals
		s.stageT0 += st.Duration
		s.stageR0 = r1
		s.stage++
	}
	return 0, false
}

// preciseWaiter sleeps until a deadline with sub-millisecond accuracy. OS
// timers can wake late (around 1ms on macOS), and in an open-model run
// that lateness would be reported as target latency. It sleeps until
// shortly before the deadline, then yields in a loop until it arrives. The
// early-wake window adapts to the timer overshoot it observes.
type preciseWaiter struct {
	timer *time.Timer
	// overshoot is an exponentially weighted average of timer lateness.
	overshoot time.Duration
}

const (
	minSpin = 100 * time.Microsecond
	maxSpin = 3 * time.Millisecond
)

func newPreciseWaiter() *preciseWaiter {
	t := time.NewTimer(time.Hour)
	t.Stop()
	return &preciseWaiter{timer: t, overshoot: time.Millisecond}
}

func (w *preciseWaiter) spin() time.Duration {
	s := 2*w.overshoot + minSpin
	return min(max(s, minSpin), maxSpin)
}

// wait blocks until deadline or until done is closed. It returns false
// when done was closed first.
func (w *preciseWaiter) wait(deadline time.Time, done <-chan struct{}) bool {
	if d := time.Until(deadline) - w.spin(); d > 0 {
		wake := time.Now().Add(d)
		w.timer.Reset(d)
		select {
		case <-w.timer.C:
		case <-done:
			w.timer.Stop()
			return false
		}
		late := time.Since(wake)
		if late < 0 {
			late = 0
		}
		w.overshoot = (w.overshoot*7 + late) / 8
	}
	for i := 0; time.Now().Before(deadline); i++ {
		if i&63 == 0 {
			select {
			case <-done:
				return false
			default:
			}
		}
		runtime.Gosched()
	}
	return true
}
