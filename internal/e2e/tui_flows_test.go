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
	d.WaitFor("Sessions")

	steps := []struct {
		key   string
		label string
	}{
		{"2", "Projects"},
		{"3", "Conversations"},
		{"4", "Notes"},
		{"5", "Agents"},
		{"6", "Settings"},
		{"7", "Network"},
		{"1", "Sessions"},
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
	d.WaitFor("Sessions")

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
	d.WaitFor("Sessions")

	// Open the form.
	d.Send("n")
	d.WaitFor("New session")

	// Cancel.
	d.Send(KeyEsc)
	d.WaitForTimeout("Sessions", 5*time.Second)

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
	d.WaitFor("Sessions")

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

// TestTUIFlow_KillSessionCancel verifies that cancelling the kill
// confirmation leaves the selected sandboxed tmux session running.
func TestTUIFlow_KillSessionCancel(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)
	e.startDaemon()

	// Pre-create a session so it shows up in the list.
	e.newTmuxSession("tui-kill-cancel", e.Home)

	// Give the daemon a moment to observe the new session.
	if !waitFor(5*time.Second, func() bool {
		return e.hasSession("tui-kill-cancel")
	}) {
		t.Fatal("pre-created session did not appear")
	}

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Sessions")

	// Wait for the session to appear in the TUI.
	d.WaitForTimeout("tui-kill-cancel", 8*time.Second)

	// Open and cancel the kill confirmation.
	d.Send("x")
	d.WaitFor("Kill session?")
	d.WaitFor("tui-kill-cancel")
	d.Send("n")
	d.Send("2")
	d.WaitForTimeout("Projects", 5*time.Second)

	if !e.hasSession("tui-kill-cancel") {
		t.Logf("TUI output:\n%s", d.Output())
		t.Fatalf("session 'tui-kill-cancel' was killed after cancel; sessions: %v", e.sessionNames())
	}

	d.Quit()
}

// TestTUIFlow_KillSessionConfirm verifies that confirming the kill modal
// removes only the selected sandboxed tmux session.
func TestTUIFlow_KillSessionConfirm(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)
	e.startDaemon()

	e.newTmuxSession("tui-kill-a-target", e.Home)
	e.newTmuxSession("tui-kill-z-keep", e.Home)

	if !waitFor(5*time.Second, func() bool {
		return e.hasSession("tui-kill-a-target") && e.hasSession("tui-kill-z-keep")
	}) {
		t.Fatal("pre-created sessions did not appear")
	}

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Sessions")
	d.WaitForTimeout("tui-kill-a-target", 8*time.Second)
	d.WaitForTimeout("tui-kill-z-keep", 8*time.Second)

	d.Send("x")
	d.WaitFor("Kill session?")
	d.WaitFor("tui-kill-a-target")
	d.Send("y")

	if !waitFor(5*time.Second, func() bool {
		return !e.hasSession("tui-kill-a-target") && e.hasSession("tui-kill-z-keep")
	}) {
		t.Logf("TUI output:\n%s", d.Output())
		t.Fatalf("kill confirmation affected wrong sessions; sessions: %v", e.sessionNames())
	}

	d.Quit()
}

// TestTUIFlow_QuitCancel verifies that cancelling the quit modal keeps
// the TUI running and leaves sandboxed tmux sessions untouched.
func TestTUIFlow_QuitCancel(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)
	e.newTmuxSession("tui-quit-cancel-keep", e.Home)

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Sessions")

	d.Send("q")
	d.WaitFor("Quit ccmux?")
	d.Send("n")
	d.Send("2")
	d.WaitForTimeout("Projects", 5*time.Second)

	if !e.hasSession("tui-quit-cancel-keep") {
		t.Fatalf("managed session was killed after quit cancel; sessions: %v", e.sessionNames())
	}

	d.Quit()
}

// TestTUIFlow_QuitConfirm verifies that confirming quit exits ccmux
// without killing sandboxed tmux sessions.
func TestTUIFlow_QuitConfirm(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)
	e.newTmuxSession("tui-quit-keep", e.Home)

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Sessions")

	d.Send("q")
	d.WaitFor("Quit ccmux?")
	d.Send("y")
	if !d.WaitForExit(5 * time.Second) {
		t.Logf("TUI output:\n%s", d.Output())
		t.Fatal("ccmux TUI did not exit after quit confirmation")
	}

	if !e.hasSession("tui-quit-keep") {
		t.Fatalf("managed session was killed after quit confirm; sessions: %v", e.sessionNames())
	}
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
	d.WaitFor("Sessions")
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
	d.WaitFor("Sessions")

	// Switch to Projects screen (key "2" after the v0.1.x tab reorder).
	d.Send("2")
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
	d.WaitForTimeout("Sessions (1)", 5*time.Second)

	// Step 3.
	d.Send(KeyEnter)
	d.WaitForTimeout("Conversations", 5*time.Second)

	// Step 4 (final slide — Enter finishes the tour).
	d.Send(KeyEnter)
	d.WaitForTimeout("Mobile", 5*time.Second)

	// Finish: Enter on the last slide closes the tour and returns to Home.
	d.Send(KeyEnter)
	d.WaitForTimeout("Sessions", 8*time.Second)

	d.Quit()
}
