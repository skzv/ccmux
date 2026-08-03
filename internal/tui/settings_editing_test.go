package tui

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// buildSettingsEditingApp returns an App parked on the Settings screen
// with the inline editor open on the projects.root field, mirroring a
// user who pressed Enter on that row and is about to type a path.
func buildSettingsEditingApp(t *testing.T) App {
	t.Helper()
	st := styles.Default()
	km := DefaultKeymap()
	cfg := config.Defaults()
	a := App{
		cfg:       cfg,
		styles:    st,
		keys:      km,
		screen:    ScreenSettings,
		width:     120,
		height:    40,
		dashboard: newDashboard(st, km),
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
		settings:  newSettings(st, km, cfg, "test"),
		network:   newNetwork(st, km),
		matrix:    newMatrix(),
	}
	var target editableField
	for _, f := range editableFields() {
		if f.label == "projects.root" {
			target = f
		}
	}
	if target.label == "" {
		t.Fatal("projects.root field missing from editableFields()")
	}
	a.settings.startEdit(target)
	if !a.settings.IsEditing() {
		t.Fatal("precondition: startEdit did not enter editing mode")
	}
	return a
}

// TestApp_SettingsEditorCapturesGlobalKeys is the regression test for
// the inline editor's keystrokes being hijacked by global handlers:
// typing a path like "~/r2qd" into projects.root fired "r" → refresh,
// "2" → jump to the Projects screen, and "q" → the quit confirmation,
// each stealing the character from the textinput. The editor must own
// every keystroke while it has focus.
func TestApp_SettingsEditorCapturesGlobalKeys(t *testing.T) {
	a := buildSettingsEditingApp(t)

	for _, k := range []string{"r", "2", "q"} {
		a, _ = updateApp(t, a, keyMsg(k))
	}

	if a.screen != ScreenSettings {
		t.Errorf("typing into the settings editor switched screens: got %v, want ScreenSettings", a.screen)
	}
	if a.confirm.open() {
		t.Error("typing 'q' into the settings editor opened the quit confirmation")
	}
	if !a.settings.IsEditing() {
		t.Error("editing mode was dropped mid-typing")
	}
	if got := a.settings.editor.Value(); !strings.HasSuffix(got, "r2q") {
		t.Errorf("typed characters did not land in the textinput: editor value = %q, want suffix %q", got, "r2q")
	}
}

// TestApp_SettingsEditorOverlayKeysSuppressed — the single-key overlay
// triggers (? help, T tour, M matrix, i info) must also go to the
// textinput while the editor has focus; modalCapturingText gates them.
func TestApp_SettingsEditorOverlayKeysSuppressed(t *testing.T) {
	a := buildSettingsEditingApp(t)

	for _, k := range []string{"?", "T", "M", "i"} {
		a, _ = updateApp(t, a, keyMsg(k))
	}

	if a.helpOpen {
		t.Error("'?' opened the help overlay over the settings editor")
	}
	if a.tour.Active() {
		t.Error("'T' opened the tour over the settings editor")
	}
	if a.matrix.Active() {
		t.Error("'M' opened the matrix overlay over the settings editor")
	}
	if a.settingsInfoOpen {
		t.Error("'i' opened the settings info overlay over the settings editor")
	}
	if got := a.settings.editor.Value(); !strings.HasSuffix(got, "?TMi") {
		t.Errorf("overlay-trigger characters did not land in the textinput: editor value = %q", got)
	}
}

// TestApp_SettingsEditorEscCancels — esc must still reach the settings
// model and cancel the edit (not get swallowed by a global handler).
func TestApp_SettingsEditorEscCancels(t *testing.T) {
	a := buildSettingsEditingApp(t)
	a, _ = updateApp(t, a, keyMsg("esc"))
	if a.settings.IsEditing() {
		t.Error("esc did not cancel the inline edit")
	}
	if a.screen != ScreenSettings {
		t.Errorf("esc moved screens: got %v", a.screen)
	}
}
