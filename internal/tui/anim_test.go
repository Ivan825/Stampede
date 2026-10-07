package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestBullDrawsInThenShowsWhole(t *testing.T) {
	m := New(nil, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.Init() == nil || !m.animating {
		t.Fatal("the intro does not start")
	}
	for i := 0; i < introSteps()+2; i++ {
		m.Update(animMsg{})
	}
	if m.intro != -1 || m.animating {
		t.Fatalf("intro %d animating %v after it should have finished", m.intro, m.animating)
	}
	view := m.view.View()
	for _, row := range bull {
		if !strings.Contains(stripANSI(view), strings.TrimRight(row, " ")) {
			t.Errorf("bull row %q not on screen:\n%s", row, stripANSI(view))
		}
	}
}

// On a short terminal the welcome stays at the top so the horns show.
func TestShortTerminalKeepsTheBullInView(t *testing.T) {
	m := New(nil, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 16})
	m.intro = -1
	m.redrawBanner()
	if first := stripANSI(strings.Split(m.view.View(), "\n")[0]); !strings.Contains(first, "█           █") {
		t.Errorf("first visible line %q, want the horns", first)
	}
}

func TestNarrowTerminalStacksTheBanner(t *testing.T) {
	wide := renderBanner(-1, 120)
	narrow := renderBanner(-1, 50)
	if lipgloss.Width(narrow) > 50 && lipgloss.Width(narrow) >= lipgloss.Width(wide) {
		t.Errorf("narrow banner is %d wide", lipgloss.Width(narrow))
	}
	if !strings.Contains(stripANSI(narrow), "STAMPEDE") || !strings.Contains(stripANSI(narrow), "█████████") {
		t.Error("narrow banner lost the mark or the name")
	}
}

func TestWorkingLine(t *testing.T) {
	m := New(nil, Options{})
	if m.busyLine() != "" {
		t.Fatal("idle console shows a working line")
	}
	m.proc = func() {}
	m.busySince = time.Now().Add(-3 * time.Second)
	line := stripANSI(m.busyLine())
	if !strings.Contains(line, "Stampeding") || !strings.Contains(line, "3s") || !strings.Contains(line, "ctrl-c") {
		t.Errorf("working line %q", line)
	}
	a := m.busyLine()
	m.frame++
	if m.busyLine() == a {
		t.Error("the working line does not change between frames")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			for i += 2; i < len(s) && (s[i] < '@' || s[i] > '~'); i++ {
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Opening the live panel shrinks the view; what was just typed stays in view.
func TestLivePanelKeepsLatestLinesInView(t *testing.T) {
	m := New(nil, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	for i := range 40 {
		m.typed = true
		m.say(fmt.Sprintf("line %d", i))
	}
	m.live = &liveRun{started: time.Now()}
	m.layout()
	if v := stripANSI(m.view.View()); !strings.Contains(v, "line 39") {
		t.Errorf("the newest line is hidden after the panel opened:\n%s", v)
	}
}
