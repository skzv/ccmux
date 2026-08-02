package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestMatrix_SkipNeoDoesNotStartSecondTick — regression for the
// duplicate-tick leak. A tick is always in flight during the Neo
// phase (open arms one, every Neo frame re-arms it). Pressing a key to
// skip Neo→Rain must NOT return a fresh tick: doing so adds a second
// concurrent tick lineage that doubles the rain frame rate and CPU for
// the life of the overlay. The skip should transition the phase and
// return a nil cmd, letting the existing in-flight tick carry on.
func TestMatrix_SkipNeoDoesNotStartSecondTick(t *testing.T) {
	m := newMatrix()
	m.Open() // addressable local → pointer-receiver Open works
	if m.phase != phaseNeo {
		t.Fatalf("precondition: Open should start in phaseNeo, got %v", m.phase)
	}

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m2.phase != phaseRain {
		t.Errorf("a key during Neo should skip to rain, got phase %v", m2.phase)
	}
	if cmd != nil {
		t.Error("skip-to-rain must not start a second tick (got non-nil cmd); the in-flight Neo tick continues the loop")
	}
}

// TestTour_TNotOpenedWhileFilterCaptmuresText — pressing capital T while
// a text-input modal (here the Projects filter) has focus must NOT
// yank the user into the first-run tour; the keystroke belongs to the
// input. Mirrors the existing modal-text guard on `i`/`u`.
func TestTour_TNotOpenedWhileFilterCapturesText(t *testing.T) {
	a := newFilterApp(t, sampleProjects())
	a = typeKeys(t, a, "/") // enter filter mode
	if !a.modalCapturingText() {
		t.Fatal("precondition: filter mode should capture text")
	}
	a = typeKeys(t, a, "T")
	if a.tour.Active() {
		t.Error("capital T during filter input must not open the tour")
	}
}
