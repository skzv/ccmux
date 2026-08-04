//go:build integration

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/config"
)

// Regression tests for the key-routing bugs fixed in PR #168
// ("fix(tui): key routing, spinner fan-out, and stale-copy fixes
// across screens"). Each test drives the real ccmux binary through a
// PTY and fails against the pre-#168 behavior.

// settingsFocusField walks the Settings cursor down until the detail
// pane paints hintMarker. A field's hint renders only while its row is
// active, so "the hint appeared after this Down" pins the cursor to
// the target row without hardcoding the row index (which shifts with
// the set of installed agents' tier rows).
func settingsFocusField(t *testing.T, d *tuiDriver, hintMarker string) {
	t.Helper()
	for i := 0; i < 15; i++ {
		mark := d.Mark()
		d.Send(KeyDown)
		if waitFor(time.Second, func() bool {
			return strings.Contains(d.OutputSince(mark), hintMarker)
		}) {
			return
		}
	}
	t.Fatalf("never reached the Settings row with hint %q; output:\n%s", hintMarker, d.Output())
}

// TestTUIFlow_SettingsEditorCapturesTypedKeys is the regression test
// for the Settings inline-editor routing bug fixed in PR #168: while
// the projects.root editor was open, "r" fired the global refresh,
// digits switched screens, and "q" opened the quit confirmation —
// so a path like "~/repos2q" could never be typed. The fix routes
// every keystroke to the editor (settingsModel.IsEditing joins
// modalCapturingText plus a dedicated routing block in App.Update).
func TestTUIFlow_SettingsEditorCapturesTypedKeys(t *testing.T) {
	e := tuiEnv(t)

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Sessions")

	// Settings screen, cursor onto projects.root (identified by its
	// hint painting in the detail pane).
	d.Send("6")
	d.WaitFor("Settings")
	settingsFocusField(t, d, "Where ccmux looks")

	// Open the inline editor.
	editMark := d.Mark()
	d.Send(KeyEnter)
	d.WaitForSince("enter to save, esc to cancel", editMark)

	// Clear the pre-filled path (Ctrl-U kills to line start in the
	// bubbles textinput) and type a value containing "r", a digit,
	// and "q" — the exact keys the pre-#168 global handlers stole.
	d.Send("\x15") // Ctrl-U
	d.Type("~/repos2q")

	// All characters must land in the editor. Pre-fix, "r" refreshed,
	// "2" jumped to Projects, and "q" opened the quit modal, so the
	// full string never rendered.
	d.WaitForSince("~/repos2q", editMark)

	// No quit-confirmation modal and no screen switch happened
	// underneath ("No projects found" / "Discovering projects" can
	// only paint if the Projects screen became active — the fixture
	// has no projects).
	d.RefuteSince(editMark, 400*time.Millisecond,
		"Quit ccmux?", "No projects found", "Discovering projects")

	// Esc cancels the edit; we are still on Settings and interactive:
	// moving the cursor paints the next field's hint, which only the
	// Settings screen renders. The pause keeps Esc and the arrow from
	// landing in one PTY read (they'd parse as an alt-chord).
	d.Send(KeyEsc)
	time.Sleep(300 * time.Millisecond)
	downMark := d.Mark()
	d.Send(KeyDown)
	d.WaitForSince("Default agent for new projects", downMark)

	// The cancelled edit must not have touched the config on disk.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Projects.Root != e.Root {
		t.Errorf("projects.root changed after cancelled edit: got %q, want %q", cfg.Projects.Root, e.Root)
	}

	d.Quit()
}

// TestTUIFlow_AgentsModelPickerSwallowsGlobalKeys is the regression
// test for the Agents picker routing bug fixed in PR #168: with the
// Claude model picker open, Tab yanked the sub-tab to Codex under the
// modal and digits switched screens. The fix makes the open picker own
// every keystroke (agentsModel.ModalOpen joins modalCapturingText, the
// sub-tab switcher is suppressed while the modal is open, and App
// routes keys straight to the Agents model).
func TestTUIFlow_AgentsModelPickerSwallowsGlobalKeys(t *testing.T) {
	e := tuiEnv(t)

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Sessions")

	// Agents screen (Claude sub-tab is the default), then open the
	// model picker with "m".
	d.Send("5")
	d.WaitFor("(tab / h·l: switch agent)")
	d.Send("m")
	d.WaitFor("Pick model")

	// Tab and a digit must be swallowed by the open picker: no Codex
	// sub-tab body, no Projects screen. Both markers can only paint
	// if the corresponding (buggy) switch actually happened.
	pickMark := d.Mark()
	d.Send(KeyTab)
	d.Send("2")
	d.RefuteSince(pickMark, 400*time.Millisecond,
		"Codex configuration", "No projects found", "Discovering projects")

	// Esc closes the picker and lands back on the Agents screen: the
	// sub-tab chrome repaints. (If "2" had leaked and switched to
	// Projects, Esc there repaints nothing Agents-flavored and this
	// wait times out.)
	escMark := d.Mark()
	d.Send(KeyEsc)
	d.WaitForSince("(tab / h·l: switch agent)", escMark)

	d.Quit()
}

// TestTUIFlow_ProjectsCommittedFilterEscClears is the regression test
// for the committed-filter bug fixed in PR #168: after typing a filter
// and pressing Enter (which blurs the input but keeps the list
// narrowed), Esc was a no-op — even though the empty state literally
// says "Press esc to clear the filter". The fix handles esc for a
// non-empty committed filter in projectsModel.Update.
func TestTUIFlow_ProjectsCommittedFilterEscClears(t *testing.T) {
	e := tuiEnv(t)
	mkProject(t, e, "alpha-proj")
	mkProject(t, e, "beta-proj")

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Sessions")

	// Projects screen with both fixtures listed.
	d.Send("2")
	d.WaitForTimeout("alpha-proj", 8*time.Second)
	d.WaitForTimeout("beta-proj", 8*time.Second)

	// Filter for something that matches nothing. Wait for filter mode
	// to engage (the live "(matches/total)" count paints) before
	// typing: a rune sent in the same PTY read as the "/" would
	// coalesce into one multi-rune key ("/z") and match no binding.
	d.Send("/")
	d.WaitFor("(2/2)")
	d.Type("zzzz")
	d.WaitFor("(0/2)")
	d.WaitFor("No projects match zzzz.")
	d.WaitFor("Press esc to clear the filter.")

	// Commit the filter. With zero matches nothing is selected, so
	// Enter must not attach/create anything either.
	d.Send(KeyEnter)
	time.Sleep(300 * time.Millisecond)
	if names := e.sessionNames(); len(names) != 0 {
		t.Fatalf("committing an empty filter created sessions: %v", names)
	}

	// Esc clears the committed filter: the full list repaints with
	// both project names. Pre-#168 esc was a no-op here, so neither
	// name ever repaints and this wait times out.
	escMark := d.Mark()
	d.Send(KeyEsc)
	d.WaitForSince("alpha-proj", escMark)
	d.WaitForSince("beta-proj", escMark)

	d.Quit()
}

// TestTUIFlow_SpinnerScreensStayResponsive covers the screens whose
// loading spinners joined the App-level spinner.TickMsg fan-out in
// PR #168 (Conversations, Notes). The pre-fix bug froze the spinner
// animation; this test asserts the stronger, non-flaky invariant that
// the screens render their chrome and keep responding to input —
// deliberately without asserting on animation frames.
func TestTUIFlow_SpinnerScreensStayResponsive(t *testing.T) {
	e := tuiEnv(t)

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Sessions")

	// Conversations: either the loading block or the loaded empty
	// state must paint (timing decides which we catch).
	convMark := d.Mark()
	d.Send("3")
	if !waitFor(8*time.Second, func() bool {
		got := d.OutputSince(convMark)
		return strings.Contains(got, "Scanning transcripts") ||
			strings.Contains(got, "No conversations for")
	}) {
		t.Fatalf("Conversations screen never rendered its chrome; output since mark:\n%s", d.OutputSince(convMark))
	}
	// Still interactive: help opens and closes. The sleeps after Esc
	// keep it from coalescing with the next key into an alt-chord in
	// a single PTY read.
	helpMark := d.Mark()
	d.Send("?")
	d.WaitForSince("switch screens", helpMark)
	d.Send(KeyEsc)
	time.Sleep(250 * time.Millisecond)

	// Notes: the no-project empty state is this screen's chrome in a
	// fixture without projects.
	notesMark := d.Mark()
	d.Send("4")
	d.WaitForSince("No project selected.", notesMark)
	helpMark2 := d.Mark()
	d.Send("?")
	d.WaitForSince("switch screens", helpMark2)
	d.Send(KeyEsc)
	time.Sleep(250 * time.Millisecond)

	// And back to Sessions — the hero banner repaints.
	homeMark := d.Mark()
	d.Send("1")
	d.WaitForSince("Hello.", homeMark)

	d.Quit()
}
