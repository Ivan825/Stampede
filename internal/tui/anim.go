package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Ivan825/Stampede/internal/version"
)

// The console animates two things: the bull drawing itself in when the
// console opens, and a working line while a command runs.

const animEvery = 70 * time.Millisecond

var (
	bright  = lipgloss.AdaptiveColor{Light: "#115E59", Dark: "#99F6E4"}
	sBright = lipgloss.NewStyle().Bold(true).Foreground(bright)
	sAccent = lipgloss.NewStyle().Foreground(accent)

	// spinFrames pulse out and back, like a star breathing.
	spinFrames = []string{"·", "✢", "✳", "✶", "✻", "✽", "✻", "✶", "✳", "✢"}
	busyVerbs  = []string{"Stampeding", "Herding", "Charging", "Rounding up", "Galloping", "Wrangling", "Kicking up dust", "Thundering"}
)

type animMsg struct{}

func animTick() tea.Cmd {
	return tea.Tick(animEvery, func(time.Time) tea.Msg { return animMsg{} })
}

// introSteps: one step per bull row drawn, then a shimmer across it.
func introSteps() int { return len(bull) + bullWidth() + 4 }

func bullWidth() int {
	w := 0
	for _, r := range bull {
		w = max(w, len([]rune(r)))
	}
	return w
}

// renderBull draws the bull at intro step: rows appear top to bottom, then
// a bright band sweeps left to right. step < 0 draws it finished.
func renderBull(step int) string {
	rows := make([]string, len(bull))
	shimmer := -10
	if step >= len(bull) {
		shimmer = step - len(bull)
	}
	for i, row := range bull {
		r := []rune(row)
		if step >= 0 && i >= step {
			rows[i] = strings.Repeat(" ", len(r))
			continue
		}
		var b strings.Builder
		for x, c := range r {
			if step >= 0 && c != ' ' && x >= shimmer-1 && x <= shimmer+1 {
				b.WriteString(sBright.Render(string(c)))
			} else {
				b.WriteString(sAccent.Render(string(c)))
			}
		}
		rows[i] = b.String()
	}
	return strings.Join(rows, "\n")
}

// renderBanner lays the bull beside the name, or above it when the
// terminal is too narrow for both, so the whole mark always shows.
func renderBanner(step, width int) string {
	art := renderBull(step)
	text := sTitle.Render("STAMPEDE") + "\n" + sMuted.Render(version.Version) + "\n\n" +
		sMuted.Render("Describe your users. Stampede becomes a thousand of them.")
	if width > 0 && width < bullWidth()+3+lipgloss.Width(text)+2 {
		return lipgloss.JoinVertical(lipgloss.Left, art, "", text) + "\n"
	}
	return lipgloss.JoinHorizontal(lipgloss.Center, art, "   ", text) + "\n"
}

// shimmerText brightens a few letters at pos, the rest in the accent.
func shimmerText(s string, pos int) string {
	var b strings.Builder
	for i, c := range []rune(s) {
		if d := i - pos; d >= -1 && d <= 1 {
			b.WriteString(sBright.Render(string(c)))
		} else {
			b.WriteString(sAccent.Render(string(c)))
		}
	}
	return b.String()
}

// busy reports whether something is running that the user waits for.
func (m *Model) busy() bool { return m.proc != nil || m.waiting > 0 }

// startAnim starts the animation clock unless it is already running.
func (m *Model) startAnim() tea.Cmd {
	if m.animating {
		return nil
	}
	m.animating = true
	return animTick()
}

// animate advances one frame and keeps the clock running while there is
// something to animate.
func (m *Model) animate() tea.Cmd {
	m.frame++
	if m.intro >= 0 {
		m.intro++
		if m.intro >= introSteps() {
			m.intro = -1
		}
		m.redrawBanner()
	}
	if m.intro < 0 && !m.busy() {
		m.animating = false
		return nil
	}
	return animTick()
}

// redrawBanner replaces the banner's lines in the transcript.
func (m *Model) redrawBanner() {
	if m.bannerAt < 0 || m.bannerAt+m.bannerLen > len(m.lines) {
		return
	}
	lines := strings.Split(renderBanner(m.intro, m.width), "\n")
	m.lines = append(append(append([]string{}, m.lines[:m.bannerAt]...), lines...), m.lines[m.bannerAt+m.bannerLen:]...)
	m.bannerLen = len(lines)
	m.view.SetContent(strings.Join(m.lines, "\n"))
	if !m.typed {
		m.view.GotoTop()
	}
}

// busyLine is the working indicator shown above the input.
func (m *Model) busyLine() string {
	if !m.busy() {
		return ""
	}
	verb := busyVerbs[(m.frame/40)%len(busyVerbs)] + "…"
	glyph := sTitle.Render(spinFrames[m.frame%len(spinFrames)])
	pos := m.frame % (len([]rune(verb)) + 6)
	secs := int(time.Since(m.busySince).Seconds())
	return glyph + " " + shimmerText(verb, pos) + sMuted.Render(fmt.Sprintf("  (%ds · ctrl-c to stop)", secs))
}

// wait marks the console busy until a remoteDoneMsg arrives.
func (m *Model) wait() tea.Cmd {
	if !m.busy() {
		m.busySince = time.Now()
	}
	m.waiting++
	return m.startAnim()
}
