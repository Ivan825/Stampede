package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func typeLine(m *Model, line string) {
	m.input.SetValue(line)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

func TestModelLocalMode(t *testing.T) {
	m := New(nil, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	typeLine(m, "/help")
	if !strings.Contains(strings.Join(m.lines, "\n"), "/run <shape>") {
		t.Error("help not shown")
	}
	typeLine(m, "/runs")
	if !strings.Contains(m.lines[len(m.lines)-1], "stampede login") {
		t.Errorf("server-only command should explain login: %q", m.lines[len(m.lines)-1])
	}
	typeLine(m, "/run spike")
	if !strings.Contains(m.lines[len(m.lines)-1], "--file") || m.live != nil {
		t.Errorf("local /run needs a file: %q", m.lines[len(m.lines)-1])
	}
	typeLine(m, "/run zigzag --file x.yaml")
	if !strings.Contains(m.lines[len(m.lines)-1], "Unknown shape") {
		t.Errorf("bad shape: %q", m.lines[len(m.lines)-1])
	}
}

func TestPlainLanguageNeedsConfirmation(t *testing.T) {
	m := New(nil, Options{})
	m.scenarios = []string{"checkout-flow"}
	typeLine(m, "find the breaking point for checkout")
	if m.pending == nil || m.pending.String() != "/run breakpoint --scenario checkout-flow" {
		t.Fatalf("pending %+v", m.pending)
	}
	typeLine(m, "n")
	if m.pending != nil || m.lines[len(m.lines)-1] != "Cancelled." {
		t.Errorf("cancel: %q", m.lines[len(m.lines)-1])
	}
}

func TestSparkline(t *testing.T) {
	if got := Sparkline([]float64{0, 1, 2, 4}, 10); got != "▁▂▄█" {
		t.Errorf("got %q", got)
	}
	if got := Sparkline([]float64{1, 2, 3}, 2); len([]rune(got)) != 2 {
		t.Errorf("width not respected: %q", got)
	}
}
