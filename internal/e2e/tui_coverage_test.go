//go:build integration

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/config"
)

// Coverage tests for TUI surfaces no existing e2e flow exercised:
// the usage overlay, the sessions preview pane, the Notes folder
// tree, terminal resize, and tour persistence across restarts.

// TestTUIFlow_UsageOverlayOpenCloseSwallowsKeys covers the `u` usage
// overlay on the Sessions screen: it opens with its detail content,
// swallows every key except its close keys (u/esc), and closing it
// restores the dashboard. The swallow check uses `p`: if the overlay
// leaked it, the sessions preview pane would toggle on underneath and
// paint its chrome after the overlay closes.
func TestTUIFlow_UsageOverlayOpenCloseSwallowsKeys(t *testing.T) {
	e := tuiEnv(t)
	e.startDaemon()
	e.newTmuxSession("tui-usage-keep", e.Home)

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Sessions")
	d.WaitForTimeout("tui-usage-keep", 10*time.Second)

	// Open the overlay.
	d.Send("u")
	d.WaitFor("Usage detail")
	d.WaitFor("press u or esc to close")

	// While open, `p` must be swallowed — the preview pane must not
	// toggle underneath. Its chrome ("capturing" / "(empty pane)" /
	// "No session selected.") never paints on any other surface.
	pMark := d.Mark()
	d.Send("p")
	d.RefuteSince(pMark, 400*time.Millisecond,
		"capturing", "(empty pane)", "No session selected.")

	// Esc closes it; the home frame repaints with the session detail
	// pane (metadata view — proves the preview did not stay toggled).
	closeMark := d.Mark()
	d.Send(KeyEsc)
	d.WaitForSince("state", closeMark)
	d.WaitForSince("attached", closeMark)
	d.RefuteSince(closeMark, 300*time.Millisecond,
		"capturing", "(empty pane)", "No session selected.")

	d.Quit()
}

// TestTUIFlow_SessionPreviewToggle covers the `p` preview pane on the
// wide Sessions screen: toggling on replaces the detail pane with a
// live capture of the selected session's tmux pane (asserted via a
// marker echoed into the real pane), toggling off restores the
// metadata view, and a rapid double-toggle doesn't wedge the UI.
func TestTUIFlow_SessionPreviewToggle(t *testing.T) {
	e := tuiEnv(t)
	e.startDaemon()
	e.newTmuxSession("tui-preview-src", e.Home)

	// Put a distinctive marker into the session's pane so the preview
	// has real content to prove it captured.
	if _, err := e.tmux("send-keys", "-t", "tui-preview-src", "echo ccmux-preview-marker-xyz", "Enter"); err != nil {
		t.Fatalf("send-keys into fixture session: %v", err)
	}
	if !waitFor(5*time.Second, func() bool {
		return strings.Contains(e.capturePane("tui-preview-src"), "ccmux-preview-marker-xyz")
	}) {
		t.Fatal("marker never appeared in the fixture session's pane")
	}

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Sessions")
	d.WaitForTimeout("tui-preview-src", 10*time.Second)

	// Toggle preview on: the right column becomes the captured pane
	// content.
	d.Send("p")
	d.WaitForTimeout("ccmux-preview-marker-xyz", 8*time.Second)

	// Toggle off: the metadata detail pane (state/path/attached)
	// repaints in its place.
	offMark := d.Mark()
	d.Send("p")
	d.WaitForSince("state", offMark)
	d.WaitForSince("attached", offMark)
	d.WaitForSince("path", offMark)

	// Rapid double-toggle must not wedge the UI: a following keypress
	// still works. The 100ms gaps keep each `p` its own key event
	// (bytes landing in one PTY read coalesce into a single key)
	// while still racing the preview's capture/tick machinery.
	d.Send("p")
	time.Sleep(100 * time.Millisecond)
	d.Send("p")
	time.Sleep(100 * time.Millisecond)
	respMark := d.Mark()
	d.Send("?")
	d.WaitForSince("switch screens", respMark)
	d.Send(KeyEsc)
	time.Sleep(200 * time.Millisecond)
	if !d.Alive() {
		t.Fatal("TUI process exited after rapid preview toggling")
	}

	d.Quit()
}

// TestTUIFlow_NotesFolderExpandCollapse covers the Notes folder tree:
// a collapsed folder row (▸) expands with Right — its child file rows
// appear — and collapses again with Left. The fixture project carries
// one root markdown file and one docs/ note so the tree shape is
// deterministic: row 0 = root file, row 1 = the docs/ folder.
func TestTUIFlow_NotesFolderExpandCollapse(t *testing.T) {
	e := tuiEnv(t)
	dir := mkProject(t, e, "notesproj")
	// H1 becomes the row's display label — keep it distinctive so the
	// assertion can't collide with any chrome text.
	writeFile(t, filepath.Join(dir, "docs", "zeta.md"), "# Zeta Note Marker\n\nbody text\n")

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Sessions")

	// Visit Projects first so the project is discovered + selected;
	// switching to Notes then adopts that selection.
	d.Send("2")
	d.WaitForTimeout("notesproj", 8*time.Second)
	notesMark := d.Mark()
	d.Send("4")
	// Tree loaded, docs/ collapsed: fold glyph ▸.
	d.WaitForSince("▸ docs/", notesMark)

	// Cursor down from the root file row onto the docs/ folder row.
	downMark := d.Mark()
	d.Send(KeyDown)
	d.WaitForSince("▸ docs/", downMark)

	// Right expands: the child file's label appears and the fold
	// glyph flips to ▾.
	expandMark := d.Mark()
	d.Send(KeyRight)
	d.WaitForSince("Zeta Note Marker", expandMark)
	d.WaitForSince("▾ docs/", expandMark)

	// Left collapses: the glyph flips back and the child row is gone.
	collapseMark := d.Mark()
	d.Send(KeyLeft)
	d.WaitForSince("▸ docs/", collapseMark)
	d.RefuteSince(collapseMark, 400*time.Millisecond, "Zeta Note Marker")

	d.Quit()
}

// TestTUIFlow_TerminalResize covers live PTY resizing: the wide
// two-column home layout at 120x40, a shrink to 60x20 (narrow layout:
// hero dropped, tab bar collapses to numeric labels), and a grow back
// — with no panic and the process still responsive throughout.
func TestTUIFlow_TerminalResize(t *testing.T) {
	e := tuiEnv(t)
	// A project gives the responsiveness check at the end something to
	// wait for that is genuinely NEW paint. The wide tab bar lists every
	// tab all the time, so "[2] Projects" is already on screen before
	// the switch — waiting for it after a mark depends on the repaint
	// happening to redraw that exact span, which differential rendering
	// doesn't promise. (It passed on macOS and failed on Linux CI.)
	// A project name only ever appears once Projects is showing.
	mkProject(t, e, "resize-probe-proj")

	d := newTUIDriver(t, e, 40, 120)
	// Wide layout: hero banner + full tab labels.
	d.WaitFor("Hello.")
	d.WaitFor("[1] Sessions")

	// Shrink. The narrow header renders the active tab as "[1 S]".
	narrowMark := d.Mark()
	d.Resize(20, 60)
	d.WaitForSince("[1 S]", narrowMark)

	// Grow back: the wide chrome returns.
	wideMark := d.Mark()
	d.Resize(40, 120)
	d.WaitForSince("Hello.", wideMark)
	d.WaitForSince("[1] Sessions", wideMark)

	// No panic leaked into the terminal at any point.
	if out := d.Output(); strings.Contains(out, "panic:") {
		t.Fatalf("TUI panicked during resize; output:\n%s", out)
	}

	// Still alive and responsive: switching screens paints the Projects
	// body, which carries content that exists on no other screen.
	respMark := d.Mark()
	d.Send("2")
	d.WaitForSince("resize-probe-proj", respMark)
	if !d.Alive() {
		t.Fatal("TUI process exited after resize sequence")
	}

	d.Quit()
}

// TestTUIFlow_TourCompletionPersists covers tour persistence across
// restarts: the first launch in a fresh env shows the tour, skipping
// it persists Tour.Shown to config.toml, and a second launch in the
// SAME env boots straight to the Sessions screen with no tour.
// (TestTUIFlow_TourNavigation covers the in-session slide flow; this
// test owns the cross-restart persistence.)
func TestTUIFlow_TourCompletionPersists(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	// Leave cfg.Tour.Shown = false so the tour fires on first launch.
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Welcome to ccmux")

	// Esc skips the tour, which marks it shown and saves the config.
	escMark := d.Mark()
	d.Send(KeyEsc)
	d.WaitForSince("[1] Sessions", escMark)

	// The completion must hit disk before we restart.
	if !waitFor(5*time.Second, func() bool {
		cfg, err := config.Load()
		return err == nil && cfg.Tour.Shown
	}) {
		t.Fatal("Tour.Shown was never persisted to config after skipping the tour")
	}

	d.Quit()

	// Second launch, same env: no tour, straight to the dashboard.
	d2 := newTUIDriver(t, e, 40, 120)
	d2.WaitFor("[1] Sessions")
	time.Sleep(500 * time.Millisecond)
	if strings.Contains(d2.Output(), "Welcome to ccmux") {
		t.Fatalf("tour re-appeared on second launch; output:\n%s", d2.Output())
	}

	d2.Quit()
}
