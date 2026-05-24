//go:build integration

package e2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestTUIFlow_ScreenNavigation verifies that the numeric keys 1–7
// switch the TUI between its seven screens.
func TestTUIFlow_ScreenNavigation(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Home")

	steps := []struct {
		key   string
		label string
	}{
		{"2", "Conversations"},
		{"3", "Projects"},
		{"4", "Notes"},
		{"5", "Agents"},
		{"6", "Settings"},
		{"7", "Network"},
		{"1", "Home"},
	}
	for _, tc := range steps {
		d.Send(tc.key)
		d.WaitForTimeout(tc.label, 8*time.Second)
	}

	d.Quit()
}

// TestTUIFlow_HelpOverlay verifies that "?" opens the help overlay
// with keybinding content.
func TestTUIFlow_HelpOverlay(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Home")

	// Open the help overlay.
	d.Send("?")
	d.WaitFor("switch screens")

	// Close it and verify the TUI is still running by sending Esc
	// and then checking that a subsequent key still produces output.
	d.Send(KeyEsc)
	// Give the TUI a moment to process the close and re-render.
	time.Sleep(200 * time.Millisecond)

	// Confirm the process is still alive by navigating away and back.
	d.Send("2")
	d.WaitForTimeout("Conversations", 5*time.Second)

	d.Quit()
}

// TestTUIFlow_NewSessionForm_OpenAndCancel verifies that pressing "n"
// on the Home screen opens the new-session form, and Esc cancels it
// without creating any tmux session.
func TestTUIFlow_NewSessionForm_OpenAndCancel(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Home")

	// Open the form.
	d.Send("n")
	d.WaitFor("New session")

	// Cancel.
	d.Send(KeyEsc)
	d.WaitForTimeout("Home", 5*time.Second)

	if names := e.sessionNames(); len(names) != 0 {
		t.Errorf("expected no tmux sessions after cancel, got %v", names)
	}

	d.Quit()
}

// TestTUIFlow_CreateBareSession verifies that the new-session form
// creates a real tmux session when submitted with a name.
func TestTUIFlow_CreateBareSession(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)
	e.startDaemon()

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Home")

	// Open the form and type a session name.
	d.Send("n")
	d.WaitFor("New session")
	d.Type("tuiflow")

	// Tab past name → workdir → device → agent, then Enter to submit.
	d.Send(KeyTab) // workdir
	d.Send(KeyTab) // device
	d.Send(KeyTab) // agent
	d.Send(KeyEnter)

	// Wait for the session to appear in tmux.
	if !waitFor(8*time.Second, func() bool {
		return e.hasSession("tuiflow")
	}) {
		t.Logf("TUI output:\n%s", d.Output())
		t.Fatalf("session 'tuiflow' did not appear in tmux within 8s; sessions: %v", e.sessionNames())
	}

	d.Quit()
}

// TestTUIFlow_KillSession verifies that pressing "x" on a session in
// the Home screen kills it in tmux immediately.
func TestTUIFlow_KillSession(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)
	e.startDaemon()

	// Pre-create a session so it shows up in the list.
	e.newTmuxSession("tui-kill-test", e.Home)

	// Give the daemon a moment to observe the new session.
	if !waitFor(5*time.Second, func() bool {
		return e.hasSession("tui-kill-test")
	}) {
		t.Fatal("pre-created session did not appear")
	}

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Home")

	// Wait for the session to appear in the TUI.
	d.WaitForTimeout("tui-kill-test", 8*time.Second)

	// Kill the selected session (no confirmation required).
	d.Send("x")

	if !waitFor(5*time.Second, func() bool {
		return !e.hasSession("tui-kill-test")
	}) {
		t.Logf("TUI output:\n%s", d.Output())
		t.Fatalf("session 'tui-kill-test' still present after kill; sessions: %v", e.sessionNames())
	}

	d.Quit()
}

// TestTUIFlow_RenameSession verifies that "R" opens the rename form,
// and submitting a new name renames the tmux session.
func TestTUIFlow_RenameSession(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)
	e.startDaemon()

	e.newTmuxSession("tui-rename-src", e.Home)

	if !waitFor(5*time.Second, func() bool {
		return e.hasSession("tui-rename-src")
	}) {
		t.Fatal("pre-created session did not appear")
	}

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Home")
	d.WaitForTimeout("tui-rename-src", 8*time.Second)

	// Open the rename form.
	d.Send("R")
	d.WaitFor("Rename")

	// Clear the pre-filled name (Ctrl-U kills to beginning of line in
	// bubbletea's textinput) then type the new name.
	d.Send("\x15") // Ctrl-U
	d.Type("tui-rename-dst")
	d.Send(KeyEnter)

	if !waitFor(5*time.Second, func() bool {
		return e.hasSession("tui-rename-dst") && !e.hasSession("tui-rename-src")
	}) {
		t.Logf("TUI output:\n%s", d.Output())
		t.Fatalf("rename did not complete; sessions: %v", e.sessionNames())
	}

	d.Quit()
}

// TestTUIFlow_ProjectsScreen verifies that a project directory with a
// CLAUDE.md appears in the Projects screen.
func TestTUIFlow_ProjectsScreen(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)

	// Create a project directory under the sandbox root.
	projDir := filepath.Join(e.Root, "my-project")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	writeFile(t, filepath.Join(projDir, "CLAUDE.md"), "# my-project\n")

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Home")

	// Switch to Projects screen.
	d.Send("3")
	d.WaitFor("Projects")

	// The project name should appear in the list.
	d.WaitForTimeout("my-project", 8*time.Second)

	d.Quit()
}

// TestTUIFlow_TourNavigation verifies the first-run tour: it auto-opens
// on first launch (Tour.Shown == false) and advances through all slides
// on Enter, returning to the main screen when finished.
func TestTUIFlow_TourNavigation(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	// Intentionally leave cfg.Tour.Shown = false so the tour fires.
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)

	d := newTUIDriver(t, e, 40, 120)

	// Step 1: Welcome slide.
	d.WaitFor("Welcome to ccmux")

	// Step 2.
	d.Send(KeyEnter)
	d.WaitForTimeout("Home (1)", 5*time.Second)

	// Step 3.
	d.Send(KeyEnter)
	d.WaitForTimeout("Conversations", 5*time.Second)

	// Step 4 (final slide — Enter finishes the tour).
	d.Send(KeyEnter)
	d.WaitForTimeout("Mobile", 5*time.Second)

	// Finish: Enter on the last slide closes the tour and returns to Home.
	d.Send(KeyEnter)
	d.WaitForTimeout("Home", 8*time.Second)

	d.Quit()
}
