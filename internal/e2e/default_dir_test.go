//go:build integration

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #234: a blank working dir must open sessions.default_dir on
// every surface — the TUI's Sessions-tab form and `ccmux shell` — and
// an edited default_dir must apply without restarting ccmuxd.

// paneStartDir is the directory session's first pane was started in,
// symlinks resolved (on macOS the sandbox's /tmp is /private/tmp).
func (e *Env) paneStartDir(session string) string {
	e.t.Helper()
	out, err := e.tmux("display-message", "-p", "-t", "="+session+":", "#{pane_start_path}")
	if err != nil {
		e.t.Fatalf("pane_start_path of %q: %v\n%s", session, err, out)
	}
	return realPath(e.t, strings.TrimSpace(out))
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("resolve %q: %v", p, err)
	}
	return r
}

// TestTUIFlow_CreateBareSession_BlankDirUsesDefaultDir is the issue's
// reproduction: Sessions tab → n → a name → Enter, working dir left
// blank. The session used to start in $HOME.
func TestTUIFlow_CreateBareSession_BlankDirUsesDefaultDir(t *testing.T) {
	e := newEnv(t)
	code := filepath.Join(e.Home, "code")
	mkdirAll(t, code)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	cfg.Sessions.DefaultDir = code
	cfg.Agents.Default = "shell" // the issue's "shell (no agent)" pick
	e.writeConfig(cfg)
	e.startDaemon()

	d := newTUIDriver(t, e, 40, 120)
	d.WaitFor("Sessions")

	d.Send("n")
	d.WaitFor("New session")
	// The placeholder promises default_dir... (textinput styles the
	// placeholder's first cell on its own, so match from the second.)
	d.WaitFor(code[1:] + " (from")
	mark := d.Mark()
	d.Type("blankdir")
	d.WaitForSince("blankdir", mark)
	d.Send(KeyEnter) // ...with the working dir left blank.

	if !waitFor(8*time.Second, func() bool { return e.hasSession("blankdir") }) {
		t.Logf("TUI output:\n%s", d.Output())
		t.Fatalf("session 'blankdir' did not appear in tmux within 8s; sessions: %v", e.sessionNames())
	}
	// ...and the session keeps it.
	if got, want := e.paneStartDir("blankdir"), realPath(t, code); got != want {
		t.Errorf("TUI session started in %q, want sessions.default_dir %q ($HOME is %q)", got, want, e.Home)
	}

	d.Quit()
}

// TestShell_DefaultDirAppliesWithoutDaemonRestart — `ccmux shell` with
// no --path opens default_dir, and an edit to default_dir applies to
// the next session while the same ccmuxd keeps running. ccmuxd used to
// keep the value it read at startup.
func TestShell_DefaultDirAppliesWithoutDaemonRestart(t *testing.T) {
	e := newEnv(t)
	first := filepath.Join(e.Home, "first")
	second := filepath.Join(e.Home, "second")
	mkdirAll(t, first)
	mkdirAll(t, second)
	cfg := e.defaultConfig()
	cfg.Sessions.DefaultDir = first
	e.writeConfig(cfg)
	e.startDaemon()

	// Without a terminal, `ccmux shell` creates the session and prints
	// how to attach instead of attaching.
	if _, stderr, err := e.ccmux("shell", "--name", "dd-first", "--agent", "shell"); err != nil {
		t.Fatalf("ccmux shell: %v\nstderr: %s", err, stderr)
	}
	if got, want := e.paneStartDir("dd-first"), realPath(t, first); got != want {
		t.Errorf("first session started in %q, want %q", got, want)
	}

	cfg.Sessions.DefaultDir = second
	e.writeConfig(cfg) // the daemon is not restarted
	if _, stderr, err := e.ccmux("shell", "--name", "dd-second", "--agent", "shell"); err != nil {
		t.Fatalf("ccmux shell: %v\nstderr: %s", err, stderr)
	}
	if got, want := e.paneStartDir("dd-second"), realPath(t, second); got != want {
		t.Errorf("after editing default_dir, session started in %q, want %q (the daemon kept its startup value?)", got, want)
	}

	// An explicit --path still wins over default_dir.
	if _, stderr, err := e.ccmux("shell", "--name", "dd-explicit", "--path", first, "--agent", "shell"); err != nil {
		t.Fatalf("ccmux shell --path: %v\nstderr: %s", err, stderr)
	}
	if got, want := e.paneStartDir("dd-explicit"), realPath(t, first); got != want {
		t.Errorf("--path session started in %q, want %q", got, want)
	}
}
