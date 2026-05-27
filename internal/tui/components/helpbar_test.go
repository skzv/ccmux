package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/tui/styles"
)

func TestHelpBar_FitsAllAtWideWidth(t *testing.T) {
	st := styles.Default()
	out := HelpBar(st, HelpBarProps{
		Hints: []KeyHint{
			{Key: "?", Label: "help", Priority: 10},
			{Key: "q", Label: "quit", Priority: 10},
			{Key: "r", Label: "refresh", Priority: 5},
			{Key: "n", Label: "new", Priority: 5},
			{Key: "x", Label: "kill", Priority: 5},
		},
		Width: 120,
	})
	plain := stripAnsi(out)
	for _, want := range []string{"help", "quit", "refresh", "new", "kill"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q at width 120: %q", want, plain)
		}
	}
}

func TestHelpBar_DropsByAscendingPriority(t *testing.T) {
	st := styles.Default()
	hints := []KeyHint{
		{Key: "?", Label: "help", Priority: 10},
		{Key: "q", Label: "quit", Priority: 10},
		{Key: "r", Label: "refresh", Priority: 1},
		{Key: "n", Label: "new", Priority: 1},
		{Key: "x", Label: "kill", Priority: 1},
	}
	// Width tight enough that low-priority hints don't fit.
	out := HelpBar(st, HelpBarProps{Hints: hints, Width: 18})
	plain := stripAnsi(out)
	for _, want := range []string{"help", "quit"} {
		if !strings.Contains(plain, want) {
			t.Errorf("high-priority hint %q dropped: %q", want, plain)
		}
	}
	for _, gone := range []string{"refresh", "new", "kill"} {
		if strings.Contains(plain, gone) {
			t.Errorf("low-priority hint %q should have been dropped: %q", gone, plain)
		}
	}
}

func TestHelpBar_NeverExceedsWidth(t *testing.T) {
	st := styles.Default()
	hints := []KeyHint{
		{Key: "?", Label: "help", Priority: 10},
		{Key: "q", Label: "quit", Priority: 9},
		{Key: "r", Label: "refresh", Priority: 5},
		{Key: "n", Label: "new", Priority: 4},
		{Key: "x", Label: "kill", Priority: 3},
		{Key: "R", Label: "rename", Priority: 2},
	}
	for _, w := range []int{120, 100, 80, 60, 40, 30, 20, 12} {
		out := HelpBar(st, HelpBarProps{Hints: hints, Width: w})
		if got := lipgloss.Width(out); got > w {
			t.Errorf("width=%d: rendered width = %d, exceeds budget", w, got)
		}
	}
}

func TestHelpBar_PreservesInputOrder(t *testing.T) {
	st := styles.Default()
	hints := []KeyHint{
		{Key: "a", Label: "alpha", Priority: 5},
		{Key: "b", Label: "beta", Priority: 1},
		{Key: "c", Label: "gamma", Priority: 5},
	}
	out := HelpBar(st, HelpBarProps{Hints: hints, Width: 200})
	plain := stripAnsi(out)
	ai := strings.Index(plain, "alpha")
	bi := strings.Index(plain, "beta")
	ci := strings.Index(plain, "gamma")
	if !(ai < bi && bi < ci) {
		t.Fatalf("hints not in input order: alpha=%d beta=%d gamma=%d in %q", ai, bi, ci, plain)
	}
}
