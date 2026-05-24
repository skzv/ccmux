package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/tui/styles"
)

func newTestApp(screen Screen) App {
	st := styles.Default()
	km := DefaultKeymap()
	return App{
		styles:    st,
		keys:      km,
		screen:    screen,
		width:     80,
		height:    24,
		dashboard: newDashboard(st, km),
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
		matrix:    newMatrix(),
	}
}

// TestApp_MatrixMKeyOpensOverlay — pressing shift-M from the navigation
// surface opens the overlay and returns a tick command.
func TestApp_MatrixMKeyOpensOverlay(t *testing.T) {
	a := newTestApp(ScreenSessions)
	m, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	a = m.(App)
	if !a.matrix.Active() {
		t.Fatal("pressing M did not activate the overlay")
	}
	if cmd == nil {
		t.Error("expected a tea.Cmd for the matrix tick, got nil")
	}
}

// TestApp_MatrixMKeyOnSessionsScreen — M works from any screen, not just Dashboard.
func TestApp_MatrixMKeyOnSessionsScreen(t *testing.T) {
	a := newTestApp(ScreenSessions)
	m, _ := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	if !m.(App).matrix.Active() {
		t.Fatal("M did not open overlay on Sessions screen")
	}
}

// TestApp_MatrixTriggerSuppressedInFormInput is the regression case for
// the reported bug: pressing M inside a text-input modal (e.g. naming a
// new session) must NOT fire the easter egg.
func TestApp_MatrixTriggerSuppressedInFormInput(t *testing.T) {
	st := styles.Default()
	km := DefaultKeymap()
	a := App{
		styles:    st,
		keys:      km,
		screen:    ScreenSessions,
		width:     80,
		height:    24,
		dashboard: newDashboard(st, km),
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
		matrix:    newMatrix(),
	}
	// Open the new-session form so the App is in form-input mode.
	form := newNewSessionForm(st, nil, "", "")
	a.sessionsM.form = &form

	m, _ := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	if m.(App).matrix.Active() {
		t.Fatal("matrix overlay fired while typing into a form — should be suppressed")
	}
}

// TestApp_MatrixEscClosesOverlay — once the overlay is open, Esc
// dismisses it and key routing returns to the regular screens.
func TestApp_MatrixEscClosesOverlay(t *testing.T) {
	a := newTestApp(ScreenSessions)
	a.matrix.Open()
	a.matrix.SetSize(80, 24)
	m, _ := a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.(App).matrix.Active() {
		t.Fatal("esc did not close the matrix overlay")
	}
}

// TestApp_MatrixQClosesOverlay — q is accepted as an alternate
// exit, mirroring esc. Tests the route through App.Update (not
// just the matrix model) because the overlay's priority routing
// in App is where regressions are most likely to hide.
func TestApp_MatrixQClosesOverlay(t *testing.T) {
	a := newTestApp(ScreenSessions)
	a.matrix.Open()
	a.matrix.SetSize(80, 24)
	// Force rain phase so the assertion proves q is intercepted
	// as an exit and not swallowed by the "any key advances" path
	// that exists only during phaseNeo.
	a.matrix.phase = phaseRain
	m, _ := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if m.(App).matrix.Active() {
		t.Fatal("q did not close the matrix overlay")
	}
}

// TestMatrix_NeoPhaseAdvancesByChar — every tick during phase 1
// advances at most one character. Drive a few ticks and assert progress.
func TestMatrix_NeoPhaseAdvancesByChar(t *testing.T) {
	m := newMatrix()
	m.Open()
	for i := 0; i < 5; i++ {
		m, _ = m.Update(matrixTickMsg{})
	}
	if m.charIdx != 5 {
		t.Errorf("after 5 ticks charIdx = %d, want 5", m.charIdx)
	}
	if m.phase != phaseNeo {
		t.Errorf("phase = %v, want phaseNeo", m.phase)
	}
}

// TestMatrix_KeyDuringNeoSkipsToRain — any non-Esc key during the
// Neo phase fast-forwards to the rain so a slow terminal doesn't
// trap the user inside the typing animation.
func TestMatrix_KeyDuringNeoSkipsToRain(t *testing.T) {
	m := newMatrix()
	m.Open()
	m.SetSize(40, 10)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if m.phase != phaseRain {
		t.Errorf("phase = %v, want phaseRain after space key", m.phase)
	}
}

// TestMatrix_ViewNeoContainsScript — the rendered Neo view should
// include the literal first line so visual regressions (e.g. a typo
// in the script) get caught.
func TestMatrix_ViewNeoContainsScript(t *testing.T) {
	m := newMatrix()
	m.Open()
	// Advance enough ticks to type the whole first line.
	for i := 0; i < len("Wake up, Neo..."); i++ {
		m, _ = m.Update(matrixTickMsg{})
	}
	out := m.View(80, 24)
	if !strings.Contains(out, "Wake up, Neo...") {
		t.Errorf("Neo view missing first line; got:\n%s", out)
	}
}

// TestMatrix_RainViewProducesGrid — once in the rain phase, View
// should return a non-empty multi-line string the size of the
// viewport.
func TestMatrix_RainViewProducesGrid(t *testing.T) {
	m := newMatrix()
	m.Open()
	m.SetSize(40, 10)
	m.phase = phaseRain
	for i := 0; i < 30; i++ {
		m, _ = m.Update(matrixTickMsg{})
	}
	out := m.View(40, 10)
	lines := strings.Split(out, "\n")
	if len(lines) != 10 {
		t.Errorf("rain view height = %d, want 10", len(lines))
	}
	// At least one glyph from the pool must appear.
	pool := string(matrixGlyphs)
	found := false
	for _, ch := range out {
		if strings.ContainsRune(pool, ch) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("rain view contains no glyphs from the pool; got:\n%s", out)
	}
}
