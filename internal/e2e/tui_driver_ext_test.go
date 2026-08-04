//go:build integration

package e2e

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Extensions to tuiDriver used by the tui_regressions / tui_coverage
// tests.
//
// The base driver (tui_pty_test.go) matches wants against the WHOLE
// accumulated PTY buffer, which is fine for "this text eventually
// appears" but useless for two kinds of assertion these tests need:
//
//   - "this repainted AFTER the action" — e.g. proving Esc restored
//     the full project list, when the project names were already in
//     the buffer from before the filter was applied;
//   - "this did NOT appear as a result of the action" — e.g. proving
//     a digit typed into the Settings editor did not switch screens.
//
// Mark/OutputSince/WaitForSince/RefuteSince add since-a-mark matching
// for both. Bubble Tea's renderer only repaints lines that changed, so
// "marker in the delta" is exactly "this content was (re)drawn by the
// action under test".

// Mark returns the current byte offset into the accumulated PTY
// output. Pass it to OutputSince / WaitForSince / RefuteSince to
// scope assertions to output produced after this moment.
func (d *tuiDriver) Mark() int { return len(d.output.String()) }

// OutputSince returns the PTY output accumulated after mark.
func (d *tuiDriver) OutputSince(mark int) string {
	s := d.output.String()
	if mark < 0 || mark > len(s) {
		return s
	}
	return s[mark:]
}

// WaitForSince polls the output that arrived after mark for want,
// failing the test after a CI-generous 8 seconds.
func (d *tuiDriver) WaitForSince(want string, mark int) {
	d.t.Helper()
	d.WaitForSinceTimeout(want, mark, 8*time.Second)
}

// WaitForSinceTimeout is WaitForSince with a caller-supplied deadline.
func (d *tuiDriver) WaitForSinceTimeout(want string, mark int, timeout time.Duration) {
	d.t.Helper()
	if !waitFor(timeout, func() bool {
		return strings.Contains(d.OutputSince(mark), want)
	}) {
		d.t.Fatalf("TUI output after mark never contained %q within %s; output since mark:\n%s",
			want, timeout, d.OutputSince(mark))
	}
}

// RefuteSince waits `settle` for the UI to process pending input, then
// fails the test if any of the markers appeared in the output produced
// after mark. Use it for "this key must have been swallowed" checks —
// the settle window is what gives the (buggy) repaint a chance to
// happen, so keep it long enough to be meaningful (~300ms+).
func (d *tuiDriver) RefuteSince(mark int, settle time.Duration, markers ...string) {
	d.t.Helper()
	time.Sleep(settle)
	got := d.OutputSince(mark)
	for _, m := range markers {
		if strings.Contains(got, m) {
			d.t.Fatalf("TUI output unexpectedly contained %q after mark; output since mark:\n%s", m, got)
		}
	}
}

// Resize changes the PTY's window size. The kernel delivers SIGWINCH
// to the TUI process, which Bubble Tea turns into a tea.WindowSizeMsg
// — the same path a real terminal resize takes.
func (d *tuiDriver) Resize(rows, cols uint16) {
	d.t.Helper()
	if err := pty.Setsize(d.f, &pty.Winsize{Rows: rows, Cols: cols}); err != nil {
		d.t.Fatalf("resize PTY to %dx%d: %v", rows, cols, err)
	}
}

// Alive reports whether the TUI process is still running (its PTY
// output stream hasn't closed).
func (d *tuiDriver) Alive() bool {
	select {
	case <-d.copyDone:
		return false
	default:
		return true
	}
}

// tuiEnv builds the standard fixture for a PTY TUI test: tour already
// shown, update auto-check off (so no network banner interferes).
func tuiEnv(t *testing.T) *Env {
	t.Helper()
	e := newEnv(t)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)
	return e
}

// mkProject creates a minimal project directory (CLAUDE.md marker)
// under the fixture's projects root so it shows up on the Projects
// screen.
func mkProject(t *testing.T, e *Env, name string) string {
	t.Helper()
	dir := e.Root + string(os.PathSeparator) + name
	writeFile(t, dir+string(os.PathSeparator)+"CLAUDE.md", "# "+name+"\n")
	return dir
}
