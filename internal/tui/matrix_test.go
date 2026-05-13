package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestMatchesMatrixTrigger covers the literal contract: the matcher
// fires only when the buffer ends in "matrix" (case-insensitive),
// because the App appends one keystroke at a time and we want the
// most recent six runes to win — partial matches mid-buffer aren't
// triggers, only completions are.
func TestMatchesMatrixTrigger(t *testing.T) {
	cases := []struct {
		buf  string
		want bool
	}{
		{"matrix", true},
		{"MATRIX", true},
		{"MaTrIx", true},
		{"hellomatrix", true},      // suffix match counts (ring buffer is bounded)
		{"matrix and more", false}, // not at the end
		{"matri", false},
		{"matrixs", false}, // extra char after
		{"", false},
	}
	for _, tc := range cases {
		if got := matchesMatrixTrigger(tc.buf); got != tc.want {
			t.Errorf("matchesMatrixTrigger(%q) = %v, want %v", tc.buf, got, tc.want)
		}
	}
}

// TestAppendTypedKey checks the ring-buffer behavior — printable
// runes accumulate, non-printable / multi-char key names clear, and
// the buffer never exceeds cap.
func TestAppendTypedKey(t *testing.T) {
	cap := 6
	buf := ""
	for _, r := range "matrix" {
		buf = appendTypedKey(buf, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}, cap)
	}
	if buf != "matrix" {
		t.Errorf("after typing 'matrix' buf = %q, want 'matrix'", buf)
	}
	// Cap respected: typing more runes drops the oldest.
	buf = appendTypedKey(buf, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}}, cap)
	if buf != "atrix!" {
		t.Errorf("post-cap drop: buf = %q, want 'atrix!'", buf)
	}
	// Non-printable resets.
	buf = appendTypedKey(buf, tea.KeyMsg{Type: tea.KeyTab}, cap)
	if buf != "" {
		t.Errorf("tab should reset buf, got %q", buf)
	}
	buf = appendTypedKey("abc", tea.KeyMsg{Type: tea.KeyEnter}, cap)
	if buf != "" {
		t.Errorf("enter should reset buf, got %q", buf)
	}
}

// TestApp_MatrixTriggerOpensOverlay drives the App through the
// "m-a-t-r-i-x" sequence and asserts the overlay opens + a tick
// command is returned so the animation can start.
func TestApp_MatrixTriggerOpensOverlay(t *testing.T) {
	st := styles.Default()
	km := DefaultKeymap()
	a := App{
		styles:    st,
		keys:      km,
		screen:    ScreenDashboard,
		width:     80,
		height:    24,
		dashboard: newDashboard(st, km),
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
		matrix:    newMatrix(),
	}
	var cmd tea.Cmd
	for _, r := range "matrix" {
		var m tea.Model
		m, cmd = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		a = m.(App)
	}
	if !a.matrix.Active() {
		t.Fatal("typing 'matrix' did not activate the overlay")
	}
	if cmd == nil {
		t.Error("expected a tea.Cmd for the matrix tick, got nil")
	}
	// Typed buffer should reset after firing so a stray "matrix"
	// later in the same session re-triggers cleanly.
	if a.typedBuf != "" {
		t.Errorf("typedBuf not cleared on trigger: %q", a.typedBuf)
	}
}

// TestApp_MatrixTriggerSuppressedInFormInput is the regression case
// for the reported bug: typing "matrix" inside a text-input modal
// (e.g. naming a new session "matrix-experiment") should NOT
// hijack the keystrokes and fire the easter egg. The trigger only
// applies to the navigation surface — the form's text field gets
// every key untouched.
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
	form := newNewSessionForm(st, nil, "")
	a.sessionsM.form = &form

	for _, r := range "matrix" {
		m, _ := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		a = m.(App)
	}
	if a.matrix.Active() {
		t.Fatal("matrix overlay fired while typing into a form — should be suppressed")
	}
	// typedBuf must also stay empty so the trigger doesn't latch on
	// after the form closes.
	if a.typedBuf != "" {
		t.Errorf("typedBuf accumulated %q during form input; should stay empty", a.typedBuf)
	}
}

// TestApp_MatrixEscClosesOverlay — once the overlay is open, Esc
// dismisses it and key routing returns to the regular screens.
func TestApp_MatrixEscClosesOverlay(t *testing.T) {
	st := styles.Default()
	km := DefaultKeymap()
	a := App{
		styles:    st,
		keys:      km,
		screen:    ScreenDashboard,
		width:     80,
		height:    24,
		dashboard: newDashboard(st, km),
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
		matrix:    newMatrix(),
	}
	a.matrix.Open()
	a.matrix.SetSize(80, 24)
	m, _ := a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a2 := m.(App)
	if a2.matrix.Active() {
		t.Fatal("esc did not close the matrix overlay")
	}
}

// TestMatrix_NeoPhaseAdvancesByChar — every tick during phase 1
// advances at most one character. Drive a few ticks and assert
// progress.
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
// viewport. We don't snapshot the rain (randomized) but we do
// assert it produced output rather than the empty placeholder.
func TestMatrix_RainViewProducesGrid(t *testing.T) {
	m := newMatrix()
	m.Open()
	m.SetSize(40, 10)
	// Force into rain phase + advance enough ticks for heads to
	// reach into the viewport.
	m.phase = phaseRain
	for i := 0; i < 30; i++ {
		m, _ = m.Update(matrixTickMsg{})
	}
	out := m.View(40, 10)
	lines := strings.Split(out, "\n")
	if len(lines) != 10 {
		t.Errorf("rain view height = %d, want 10", len(lines))
	}
	// Confirm at least one glyph from the pool appears somewhere.
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
