package report

import (
	"fmt"
	"html"
	"html/template"
	"math"
	"strings"
)

// series is one line on a chart.
type series struct {
	Name  string
	Class string // CSS class controlling colour
	Ys    []float64
	Fmt   func(float64) string
	// Right puts the series on the right-hand axis.
	Right bool
}

// lineChart renders a responsive SVG line chart. xs are seconds.
func lineChart(title string, xs []float64, ss []series) template.HTML {
	const (
		w, h         = 760.0, 240.0
		padL, padR   = 56.0, 56.0
		padT, padB   = 16.0, 28.0
		plotW, plotH = w - padL - padR, h - padT - padB
		gridLines    = 4
	)
	if len(xs) == 0 {
		return template.HTML(`<p class="empty">No data recorded.</p>`)
	}
	xMax := xs[len(xs)-1]
	if xMax <= 0 {
		xMax = 1
	}
	maxOf := func(right bool) float64 {
		m := 0.0
		for _, s := range ss {
			if s.Right != right {
				continue
			}
			for _, y := range s.Ys {
				if !math.IsNaN(y) && y > m {
					m = y
				}
			}
		}
		return niceCeil(m)
	}
	lMax, rMax := maxOf(false), maxOf(true)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s" preserveAspectRatio="none">`, w, h, html.EscapeString(title))
	// Grid and left/right axis labels.
	var lFmt, rFmt func(float64) string
	for _, s := range ss {
		if s.Right && rFmt == nil {
			rFmt = s.Fmt
		}
		if !s.Right && lFmt == nil {
			lFmt = s.Fmt
		}
	}
	for i := 0; i <= gridLines; i++ {
		y := padT + plotH*float64(i)/gridLines
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, padL, padL+plotW, y, y)
		frac := 1 - float64(i)/gridLines
		if lFmt != nil {
			fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, padL-6, y+4, html.EscapeString(lFmt(lMax*frac)))
		}
		if rFmt != nil && rMax > 0 {
			fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f">%s</text>`, padL+plotW+6, y+4, html.EscapeString(rFmt(rMax*frac)))
		}
	}
	for i := 0; i <= 5; i++ {
		x := padL + plotW*float64(i)/5
		fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, x, h-8, fmtSecs(xMax*float64(i)/5))
	}
	for _, s := range ss {
		top := lMax
		if s.Right {
			top = rMax
		}
		if top <= 0 {
			top = 1
		}
		var pts []string
		for i, y := range s.Ys {
			if i >= len(xs) || math.IsNaN(y) {
				continue
			}
			px := padL + plotW*xs[i]/xMax
			py := padT + plotH*(1-y/top)
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", px, py))
		}
		fmt.Fprintf(&b, `<polyline class="line %s" points="%s"/>`, s.Class, strings.Join(pts, " "))
	}
	b.WriteString(`</svg><div class="legend">`)
	for _, s := range ss {
		side := ""
		if s.Right {
			side = " (right axis)"
		}
		fmt.Fprintf(&b, `<span><i class="swatch %s"></i>%s%s</span>`, s.Class, html.EscapeString(s.Name), side)
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String()) //nolint:gosec // all text is escaped above
}

func niceCeil(v float64) float64 {
	if v <= 0 {
		return 1
	}
	exp := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10} {
		if m*exp >= v {
			return m * exp
		}
	}
	return 10 * exp
}

func fmtSecs(s float64) string {
	switch {
	case s >= 3600:
		return fmt.Sprintf("%.1fh", s/3600)
	case s >= 120:
		return fmt.Sprintf("%.0fm", s/60)
	default:
		return fmt.Sprintf("%.0fs", s)
	}
}
