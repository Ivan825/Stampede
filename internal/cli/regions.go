package cli

import (
	"fmt"
	"strings"

	"github.com/Ivan825/Stampede/internal/scenario"
)

// regionFlagHelp describes the repeatable --region flag.
const regionFlagHelp = "split the load by worker region, REGION=PERCENT (repeatable, adds up to 100%), e.g. --region mumbai=50% --region frankfurt=50%; replaces load.regions"

// regionsFrom parses repeated --region REGION=PERCENT flags into the
// override the API takes: percent (0-100) per region. It returns nil when
// no flag was given.
func regionsFrom(flags []string) (map[string]float64, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	split := map[string]scenario.Percent{}
	for _, f := range flags {
		name, val, ok := strings.Cut(f, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("--region %q: use REGION=PERCENT, such as mumbai=50%%", f)
		}
		val = strings.TrimSpace(val)
		if !strings.HasSuffix(val, "%") {
			val += "%"
		}
		p, err := scenario.ParsePercent(val)
		if err != nil {
			return nil, fmt.Errorf("--region %q: %w", f, err)
		}
		if _, dup := split[name]; dup {
			return nil, fmt.Errorf("--region %s is given twice", name)
		}
		split[name] = p
	}
	if probs := scenario.ValidateRegions(split); len(probs) > 0 {
		return nil, fmt.Errorf("--region: %s", strings.Join(probs, "; "))
	}
	out := make(map[string]float64, len(split))
	for r, p := range split {
		out[r] = float64(p) * 100
	}
	return out, nil
}
