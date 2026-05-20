package tui

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/tui/styles"
)

func TestTour_ZeroValueInactive(t *testing.T) {
	var m tourModel
	if m.Active() {
		t.Fatal("zero tourModel should be inactive")
	}
	if got := m.View(80, 24); got != "" {
		t.Fatalf("inactive tour should render empty; got %q", got[:min(40, len(got))])
	}
}

func TestTour_OpenAndClose(t *testing.T) {
	m := newTour(styles.Default())
	m.Open()
	if !m.Active() {
		t.Fatal("Open should activate")
	}
	if m.Step() != 0 {
		t.Errorf("Open should reset to step 0, got %d", m.Step())
	}
	m.Close()
	if m.Active() {
		t.Fatal("Close should deactivate")
	}
}

func TestTour_NextAdvancesUntilLast(t *testing.T) {
	m := newTour(styles.Default())
	m.Open()
	steps := len(m.steps)
	for i := 0; i < steps-1; i++ {
		if !m.Next() {
			t.Fatalf("Next at step %d should advance, returned false", m.Step())
		}
	}
	// We're now on the last step; Next must return false (signals "done").
	if m.Next() {
		t.Fatal("Next on the last step should return false (signals 'finished')")
	}
	// Step shouldn't have overflowed.
	if m.Step() != steps-1 {
		t.Errorf("after exhaustion step=%d, want %d", m.Step(), steps-1)
	}
}

func TestTour_PrevDoesNotGoBelowZero(t *testing.T) {
	m := newTour(styles.Default())
	m.Open()
	m.Prev()
	if m.Step() != 0 {
		t.Errorf("Prev at step 0 should stay at 0, got %d", m.Step())
	}
	m.Next()
	m.Prev()
	if m.Step() != 0 {
		t.Errorf("Next then Prev should land at 0, got %d", m.Step())
	}
}

func TestTour_ViewIncludesTitleAndKeyHint(t *testing.T) {
	m := newTour(styles.Default())
	m.Open()
	out := m.View(100, 30)
	if out == "" {
		t.Fatal("active tour should render non-empty")
	}
	first := m.steps[0]
	if !strings.Contains(out, first.Title) {
		t.Errorf("view missing title %q", first.Title)
	}
	if first.KeyHint != "" && !strings.Contains(out, first.KeyHint) {
		t.Errorf("view missing key hint %q", first.KeyHint)
	}
}

// TestDefaultTourSteps_ContainsRequiredAnchors locks in that the script
// hits the screens we promise in the README. If the tour ever drops one
// of the four main screens, this fires to force a conversation.
func TestDefaultTourSteps_ContainsRequiredAnchors(t *testing.T) {
	steps := defaultTourSteps()
	if len(steps) < 3 {
		t.Fatalf("tour has %d steps, expected at least 3", len(steps))
	}
	all := ""
	for _, s := range steps {
		all += s.Title + "\n"
		for _, b := range s.Body {
			all += b + "\n"
		}
		for _, b := range s.Bullets {
			all += b + "\n"
		}
	}
	for _, must := range []string{"Home", "Sessions", "Projects", "Notes"} {
		if !strings.Contains(all, must) {
			t.Errorf("tour script missing reference to %q screen", must)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
