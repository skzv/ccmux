package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tui/styles"
)

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
