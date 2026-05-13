package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// selName is a nil-safe helper for test error messages.
func selName(s *daemon.SessionState) string {
	if s == nil {
		return "<nil>"
	}
	return s.Name
}

// TestApp_SessionsFormInterceptsEnter is the regression test for the
// bug where pressing Enter inside the Sessions tab's new-session form
// attached to the highlighted session (commonly `c-ccmux`) instead of
// submitting the form. Cause: the App's global Enter handler fired
// before the modal form had a chance to consume the key, because the
// modal-routing escape-hatch existed only for the Projects screen.
//
// Contract under test: when the Sessions form is open and Enter is
// pressed, the App must return a command whose payload is a
// newBareSessionSubmitMsg — NOT an attach side effect.
func TestApp_SessionsFormInterceptsEnter(t *testing.T) {
	st := styles.Default()
	km := DefaultKeymap()

	// Build an App by hand: New() does claude auth detection + a 3s
	// timeout which would slow tests down and add a network dep. We
	// only need enough state for the key router to make its routing
	// decision.
	a := App{
		styles:    st,
		keys:      km,
		screen:    ScreenSessions,
		sessionsM: newSessions(st, km),
		// Other screens get zero-valued models. They're not touched
		// because the screen is fixed to Sessions and the modal-
		// routing branch returns before any of them run.
		projectsM: newProjects(st, km),
	}

	// Seed a session named c-ccmux so the Sessions cursor has
	// somewhere to land. Without this, attachSelectedSession() would
	// no-op (Selected() returns nil) and the bug would be invisible.
	a.sessionsM.SetSessions([]daemon.SessionState{{Name: "c-ccmux", Host: "local"}})

	// Open the new-session form the same way the `n` key would.
	form := newNewSessionForm(st, nil, "")
	a.sessionsM.form = &form

	// Press Enter.
	updated, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	a2 := updated.(App)
	if cmd == nil {
		t.Fatal("expected a tea.Cmd, got nil")
	}
	got := cmd()

	if _, ok := got.(newBareSessionSubmitMsg); !ok {
		// The other plausible outcome for an unfixed App is a tea exec
		// for the attach; we don't try to inspect that — just assert
		// the positive case so the test stays robust against new exec
		// shapes.
		t.Fatalf("expected newBareSessionSubmitMsg from form Enter, got %T (cmd output: %#v)", got, got)
	}

	// Sanity: the sessions screen should still hold the form after
	// the submit (the App's submit handler clears it, not this
	// pathway).
	if a2.sessionsM.form == nil {
		t.Error("modal form was cleared by Enter; submit clearing should happen in App.Update's submit handler, not here")
	}
}

// TestApp_SessionsFormInterceptsScreenKeys — second regression: the
// global handler also routes digit keys (1, 2, 3, …) for screen
// switching. If those fire while the form's text input is focused,
// typing "2" in the name field would switch the user to the Sessions
// tab instead of inserting the digit. Same root cause, same fix.
func TestApp_SessionsFormInterceptsScreenKeys(t *testing.T) {
	st := styles.Default()
	km := DefaultKeymap()
	a := App{
		styles:    st,
		keys:      km,
		screen:    ScreenSessions,
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
	}
	form := newNewSessionForm(st, nil, "")
	a.sessionsM.form = &form

	updated, _ := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	a2 := updated.(App)
	if a2.screen != ScreenSessions {
		t.Errorf("digit while form open switched screen to %v; expected to stay on ScreenSessions", a2.screen)
	}
	if a2.sessionsM.form == nil {
		t.Error("digit while form open dismissed the form")
	}
}

// newSessionsApp builds a minimal App wired to the Sessions screen,
// avoiding the network call inside New(). Used by the session-selection
// invariant tests below.
func newSessionsApp(t *testing.T) App {
	t.Helper()
	st := styles.Default()
	km := DefaultKeymap()
	return App{
		styles:    st,
		keys:      km,
		screen:    ScreenSessions,
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
		matrix:    newMatrix(),
	}
}

// sendSessions delivers a sessionsLoadedMsg through App.Update, returning
// the updated App. This is the same path the real 2-second poller uses.
func sendSessions(t *testing.T, a App, ss []daemon.SessionState) App {
	t.Helper()
	m, _ := a.Update(sessionsLoadedMsg{Sessions: ss, At: time.Now()})
	return m.(App)
}

// navigate sends n Down-arrow key presses through App.Update.
func navigate(t *testing.T, a App, n int) App {
	t.Helper()
	for i := 0; i < n; i++ {
		m, _ := a.Update(tea.KeyMsg{Type: tea.KeyDown})
		a = m.(App)
	}
	return a
}

// --------------------------------------------------------------------
// Wrong-session-join invariant tests
//
// These tests guard the invariant: the session the user navigated to
// must remain Selected() after a refresh, regardless of how the
// session list is ordered or stored internally. The tests exercise the
// full App.Update message pipeline (sessionsLoadedMsg + key events)
// rather than calling SetSessions directly, so they remain valid even
// if the session picker is refactored or replaced.
// --------------------------------------------------------------------

// TestApp_SelectionSurvivesListReorder is the canonical reproduction of
// the reported bug: user selects c-ccmux, refresh reorders the list
// (c-ccmux-website now first), Enter should still join c-ccmux.
func TestApp_SelectionSurvivesListReorder(t *testing.T) {
	a := newSessionsApp(t)
	a = sendSessions(t, a, []daemon.SessionState{
		{Name: "c-ccmux", Host: "local"},
		{Name: "c-ccmux-website", Host: "local"},
	})

	// User is already on c-ccmux (cursor 0, first row).
	if got := selName(a.sessionsM.Selected()); got != "c-ccmux" {
		t.Fatalf("initial selection = %q, want c-ccmux", got)
	}

	// Refresh fires: list comes back in reversed order.
	// Root cause of the bug: c-ccmux-website is now at index 0, and
	// without name-tracking the cursor would silently shift to it.
	a = sendSessions(t, a, []daemon.SessionState{
		{Name: "c-ccmux-website", Host: "local"},
		{Name: "c-ccmux", Host: "local"},
	})

	got := a.sessionsM.Selected()
	if got == nil || got.Name != "c-ccmux" {
		t.Errorf("after reorder, Selected() = %q, want c-ccmux — cursor drifted to wrong session",
			selName(got))
	}
}

// TestApp_SelectionSurvivesNewSessionInsertedAbove covers the case
// where a new session whose name sorts above the current selection is
// created on another device and shows up in the next refresh.
func TestApp_SelectionSurvivesNewSessionInsertedAbove(t *testing.T) {
	a := newSessionsApp(t)
	a = sendSessions(t, a, []daemon.SessionState{
		{Name: "c-website", Host: "local"},
		{Name: "c-work", Host: "local"},
	})
	// Navigate to c-work (index 1).
	a = navigate(t, a, 1)
	if got := selName(a.sessionsM.Selected()); got != "c-work" {
		t.Fatalf("pre-refresh selection = %q, want c-work", got)
	}

	// Refresh: a new session c-alpha appears, sorts to the top.
	// Without name-tracking, cursor 1 would now point to c-website.
	a = sendSessions(t, a, []daemon.SessionState{
		{Name: "c-alpha", Host: "local"},
		{Name: "c-website", Host: "local"},
		{Name: "c-work", Host: "local"},
	})

	got := a.sessionsM.Selected()
	if got == nil || got.Name != "c-work" {
		t.Errorf("after new session inserted above, Selected() = %q, want c-work",
			selName(got))
	}
}

// TestApp_SelectionFallsBackWhenSessionKilled ensures that when the
// currently-selected session disappears (killed remotely), the cursor
// does not go out of bounds and Selected() returns something valid.
func TestApp_SelectionFallsBackWhenSessionKilled(t *testing.T) {
	a := newSessionsApp(t)
	a = sendSessions(t, a, []daemon.SessionState{
		{Name: "c-alpha", Host: "local"},
		{Name: "c-beta", Host: "local"},
		{Name: "c-gamma", Host: "local"},
	})
	// Navigate to the last session.
	a = navigate(t, a, 2)
	if got := selName(a.sessionsM.Selected()); got != "c-gamma" {
		t.Fatalf("pre-refresh selection = %q, want c-gamma", got)
	}

	// c-gamma is killed externally; next refresh omits it.
	a = sendSessions(t, a, []daemon.SessionState{
		{Name: "c-alpha", Host: "local"},
		{Name: "c-beta", Host: "local"},
	})

	got := a.sessionsM.Selected()
	if got == nil {
		t.Fatal("Selected() = nil after selected session killed — should fall back to a valid session")
	}
	// Index must be in bounds.
	if a.sessionsM.cursor < 0 || a.sessionsM.cursor >= len(a.sessionsM.sessions) {
		t.Errorf("cursor %d out of bounds after kill (len=%d)", a.sessionsM.cursor, len(a.sessionsM.sessions))
	}
}

// TestApp_SelectionStableUnderRepeatedIdenticalRefreshes — sanity check
// that stable refreshes (same list, same order) don't move the cursor.
func TestApp_SelectionStableUnderRepeatedIdenticalRefreshes(t *testing.T) {
	sessions := []daemon.SessionState{
		{Name: "c-ccmux", Host: "local"},
		{Name: "c-ccmux-website", Host: "local"},
	}
	a := newSessionsApp(t)
	a = sendSessions(t, a, sessions)
	a = navigate(t, a, 1) // select c-ccmux-website

	for i := 0; i < 5; i++ {
		a = sendSessions(t, a, sessions)
		if got := selName(a.sessionsM.Selected()); got != "c-ccmux-website" {
			t.Errorf("after refresh #%d, Selected() = %q, want c-ccmux-website", i+1, got)
		}
	}
}
