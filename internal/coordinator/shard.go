package coordinator

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Ivan825/Stampede/internal/scenario"
)

// share is a worker's slice [lo, hi) of the total load. The engine maps
// every arrival (and every planned user) to exactly one slice, so slices
// that tile [0, 1) reproduce the plan exactly.
type share struct{ lo, hi float64 }

// computeShares splits [0, 1) into contiguous slices proportional to
// weights. Boundaries are cumulative sums, the first slice starts at
// exactly 0 and the last ends at exactly 1, so the slices always tile the
// interval with no gap or overlap regardless of rounding.
func computeShares(weights []float64) ([]share, error) {
	if len(weights) == 0 {
		return nil, errors.New("no workers")
	}
	var total float64
	for i, w := range weights {
		if !(w > 0) || math.IsInf(w, 0) {
			return nil, fmt.Errorf("worker %d has invalid weight %v", i, w)
		}
		total += w
	}
	out := make([]share, len(weights))
	var cum float64
	for i, w := range weights {
		out[i].lo = cum / total
		if i > 0 {
			out[i].lo = out[i-1].hi
		}
		cum += w
		out[i].hi = cum / total
		if i == len(weights)-1 {
			out[i].hi = 1
		}
		if out[i].hi <= out[i].lo {
			return nil, fmt.Errorf("worker %d's share is too small to represent", i)
		}
	}
	return out, nil
}

// candidate is a worker that may take part in a run.
type candidate struct {
	id     string
	name   string
	region string
	cpus   float64
	maxVUs int
}

// selection is the outcome of choosing workers for a run.
type selection struct {
	workers []candidate
	shares  []share
}

// selectWorkers picks the workers for a run and their shares.
//
// Without regions, the n workers with the most CPUs are used (all of them
// when n is 0) and load is split in proportion to CPUs. With regions,
// each region gets its fraction of the load (fractions are normalised),
// split among that region's workers by CPUs; n, when set, is spread over
// the regions in proportion to their fractions with at least one each.
func selectWorkers(avail []candidate, n int, regions map[string]float64, plan *scenario.Plan) (*selection, error) {
	if n < 0 {
		return nil, fmt.Errorf("workers must not be negative, got %d", n)
	}
	sorted := append([]candidate(nil), avail...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].cpus != sorted[j].cpus {
			return sorted[i].cpus > sorted[j].cpus
		}
		return sorted[i].id < sorted[j].id
	})

	var (
		chosen  []candidate
		weights []float64
	)
	if len(regions) == 0 {
		if len(sorted) == 0 {
			return nil, errors.New("not enough capacity: no workers are connected and idle")
		}
		if n == 0 {
			n = len(sorted)
		}
		if n > len(sorted) {
			return nil, fmt.Errorf("not enough capacity: %d workers requested but only %d are connected and idle", n, len(sorted))
		}
		chosen = sorted[:n]
		for _, c := range chosen {
			weights = append(weights, c.cpus)
		}
	} else {
		names := make([]string, 0, len(regions))
		var fsum float64
		for r, f := range regions {
			if !(f > 0) || math.IsInf(f, 0) {
				return nil, fmt.Errorf("region %q has invalid fraction %v", r, f)
			}
			names = append(names, r)
			fsum += f
		}
		sort.Strings(names)
		byRegion := map[string][]candidate{}
		for _, c := range sorted {
			byRegion[c.region] = append(byRegion[c.region], c)
		}
		counts, err := regionCounts(names, regions, fsum, n, byRegion)
		if err != nil {
			return nil, err
		}
		for _, r := range names {
			rs := byRegion[r][:counts[r]]
			var cpus float64
			for _, c := range rs {
				cpus += c.cpus
			}
			for _, c := range rs {
				chosen = append(chosen, c)
				weights = append(weights, regions[r]/fsum*c.cpus/cpus)
			}
		}
	}

	shares, err := computeShares(weights)
	if err != nil {
		return nil, err
	}
	if err := checkVUCapacity(chosen, shares, plan); err != nil {
		return nil, err
	}
	return &selection{workers: chosen, shares: shares}, nil
}

// regionCounts decides how many workers each region contributes.
func regionCounts(names []string, regions map[string]float64, fsum float64, n int, byRegion map[string][]candidate) (map[string]int, error) {
	counts := map[string]int{}
	var short []string
	for _, r := range names {
		if len(byRegion[r]) == 0 {
			short = append(short, fmt.Sprintf("%s (none)", r))
		}
	}
	if len(short) > 0 {
		return nil, fmt.Errorf("not enough capacity: no idle workers in region %s", strings.Join(short, ", "))
	}
	if n == 0 {
		for _, r := range names {
			counts[r] = len(byRegion[r])
		}
		return counts, nil
	}
	if n < len(names) {
		return nil, fmt.Errorf("not enough workers: %d requested for %d regions (at least one each)", n, len(names))
	}
	// Largest remainder, with at least one worker per region.
	type rem struct {
		r string
		f float64
	}
	var rems []rem
	used := 0
	for _, r := range names {
		exact := float64(n) * regions[r] / fsum
		c := max(1, int(math.Floor(exact)))
		counts[r] = c
		used += c
		rems = append(rems, rem{r, exact - math.Floor(exact)})
	}
	sort.SliceStable(rems, func(i, j int) bool { return rems[i].f > rems[j].f })
	for i := 0; used < n; i = (i + 1) % len(rems) {
		counts[rems[i].r]++
		used++
	}
	for used > n {
		// The at-least-one rule overshot: take from the largest region.
		big := names[0]
		for _, r := range names {
			if counts[r] > counts[big] {
				big = r
			}
		}
		counts[big]--
		used--
	}
	for _, r := range names {
		if counts[r] > len(byRegion[r]) {
			short = append(short, fmt.Sprintf("%s (%d requested, %d idle)", r, counts[r], len(byRegion[r])))
		}
	}
	if len(short) > 0 {
		return nil, fmt.Errorf("not enough capacity in region %s", strings.Join(short, ", "))
	}
	return counts, nil
}

// requiredVUs is the most virtual users the plan runs at once.
func requiredVUs(p *scenario.Plan) float64 {
	switch p.Executor {
	case scenario.ExecConstantRate, scenario.ExecRampingRate, scenario.ExecReplay:
		return float64(p.MaxVUs)
	case scenario.ExecIterations:
		return float64(p.VUs)
	default:
		return p.Peak()
	}
}

// checkVUCapacity rejects a split that gives a worker more virtual users
// than it accepts. The count mirrors the engine's own share rounding.
func checkVUCapacity(ws []candidate, shares []share, plan *scenario.Plan) error {
	if plan == nil {
		return nil
	}
	need := requiredVUs(plan)
	var over []string
	for i, w := range ws {
		got := int(math.Round(need*shares[i].hi)) - int(math.Round(need*shares[i].lo))
		if w.maxVUs > 0 && got > w.maxVUs {
			over = append(over, fmt.Sprintf("%s would run %d virtual users but accepts %d", w.name, got, w.maxVUs))
		}
	}
	if len(over) > 0 {
		return fmt.Errorf("not enough capacity: %s", strings.Join(over, "; "))
	}
	return nil
}
