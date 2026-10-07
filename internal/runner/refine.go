package runner

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// RefineRounds is how many confirmation holds narrow a breakpoint once a
// level fails: each halves the gap between the last level that held and
// the first that failed.
const RefineRounds = 3

// Round runs one confirmation hold at level and reports whether every
// target held, and which failed if not.
type Round func(ctx context.Context, level float64) (pass bool, failedOn []string, err error)

// Refine narrows a found breakpoint by bisection. The breakpoint's
// LastPass and FirstFail become the tightest bracket found, and each
// round is listed in Refined. A round that errors stops the refinement
// and keeps what was learned.
func Refine(ctx context.Context, bp *report.Breakpoint, mode string, rounds int, run Round) error {
	if bp == nil || !bp.Found || rounds <= 0 {
		return nil
	}
	lo, hi := bp.LastPass, bp.FirstFail
	for range rounds {
		mid := (lo + hi) / 2
		if mode != scenario.ModeRate {
			mid = math.Round(mid) // users are whole
		} else {
			mid = math.Round(mid*10) / 10
		}
		if mid <= lo || mid >= hi {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		pass, failedOn, err := run(ctx, mid)
		if err != nil {
			return err
		}
		bp.Refined = append(bp.Refined, report.RefineStep{Level: mid, Pass: pass, FailedOn: failedOn})
		if pass {
			lo = mid
		} else {
			hi, bp.FailedOn = mid, failedOn
		}
	}
	bp.LastPass, bp.FirstFail = lo, hi
	return nil
}

// ConfirmScenario is s with its load replaced by one breakpoint step at
// level: the original ramp, then half the original hold, but at least
// 15s (or the whole hold when that is shorter).
func ConfirmScenario(s *scenario.Scenario, plan *scenario.Plan, level float64) (*scenario.Scenario, *scenario.Plan, error) {
	if len(plan.Stages) < 2 {
		return nil, nil, fmt.Errorf("not a breakpoint plan")
	}
	h := plan.Stages[1].Duration
	ramp, hold := plan.Stages[0].Duration, max(h/2, min(h, 15*time.Second))
	c := *s
	target := strconv.FormatFloat(level, 'f', -1, 64)
	if plan.Mode == scenario.ModeRate {
		target += "/s"
	}
	c.Load = scenario.Load{
		Mode:         plan.Mode,
		Stages:       []scenario.Stage{{Duration: scenario.Duration(ramp), Target: target}, {Duration: scenario.Duration(hold), Target: target}},
		MaxVUs:       s.Load.MaxVUs,
		GracefulStop: scenario.Duration(min(plan.GracefulStop, 10*time.Second)),
	}
	p, err := c.Load.Plan()
	if err != nil {
		return nil, nil, err
	}
	return &c, p, nil
}
