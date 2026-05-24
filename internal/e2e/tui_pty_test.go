//go:build integration

package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Key sequence constants for PTY input.
const (
	KeyUp        = "\x1b[A"
	KeyDown      = "\x1b[B"
	KeyRight     = "\x1b[C"
	KeyLeft      = "\x1b[D"
	KeyEnter     = "\r"
	KeyEsc       = "\x1b"
	KeyCtrlC     = "\x03"
	KeyBackspace = "\x7f"
	KeyTab       = "\t"
)

// tuiDriver drives a running ccmux TUI through a PTY.
// Construct with newTUIDriver; the process is killed on t.Cleanup.
type tuiDriver struct {
	t        *testing.T
	f        *os.File
	cmd      *exec.Cmd
	output   *safeBuffer
	copyDone chan struct{}
}

// newTUIDriver starts ccmux in a PTY with the sandbox env.
// rows/cols are terminal dimensions; 40/120 are sensible defaults.
// The process and PTY are registered for cleanup via t.Cleanup.
func newTUIDriver(t *testing.T, e *Env, rows, cols uint16) *tuiDriver {
	t.Helper()

	cmd := exec.Command(builtCcmux)
	cmd.Dir = e.Home
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		t.Fatalf("start ccmux TUI in PTY: %v", err)
	}

	output := &safeBuffer{}
	copyDone := make(chan struct{})
	go copyTerminalOutput(f, output, copyDone)

	d := &tuiDriver{
		t:        t,
		f:        f,
		cmd:      cmd,
		output:   output,
		copyDone: copyDone,
	}
	t.Cleanup(d.close)
	return d
}

// close shuts the PTY and kills the process. Called via t.Cleanup.
func (d *tuiDriver) close() {
	_ = d.f.Close()
	if d.cmd.Process != nil {
		_ = d.cmd.Process.Kill()
		_, _ = d.cmd.Process.Wait()
	}
	select {
	case <-d.copyDone:
	case <-time.After(3 * time.Second):
	}
}

// Send writes a raw byte sequence to the PTY (use key constants or
// literal characters).
func (d *tuiDriver) Send(key string) {
	d.t.Helper()
	writeTTY(d.t, d.f, key)
}

// Type sends each character in s with a 20ms gap to simulate typing.
func (d *tuiDriver) Type(s string) {
	d.t.Helper()
	for _, ch := range s {
		writeTTY(d.t, d.f, string(ch))
		time.Sleep(20 * time.Millisecond)
	}
}

// WaitFor polls accumulated PTY output for want, failing the test
// after 5 seconds.
func (d *tuiDriver) WaitFor(want string) {
	d.t.Helper()
	d.WaitForTimeout(want, 5*time.Second)
}

// WaitForTimeout is WaitFor with a caller-supplied deadline.
func (d *tuiDriver) WaitForTimeout(want string, timeout time.Duration) {
	d.t.Helper()
	if !waitFor(timeout, func() bool {
		return strings.Contains(d.output.String(), want)
	}) {
		d.t.Fatalf("TUI output never contained %q within %s; output:\n%s",
			want, timeout, d.output.String())
	}
}

// WaitForWithInput retries sending input until want appears in output.
// Mirrors waitForTUIWithInput from the harness.
func (d *tuiDriver) WaitForWithInput(want, input string) {
	d.t.Helper()
	waitForTUIWithInput(d.t, d.f, d.output, want, input)
}

// Output returns a snapshot of the accumulated PTY output.
func (d *tuiDriver) Output() string {
	return d.output.String()
}

// Quit sends Ctrl-C and waits for the TUI process to exit.
func (d *tuiDriver) Quit() {
	d.t.Helper()
	writeTTY(d.t, d.f, KeyCtrlC)
	select {
	case <-d.copyDone:
	case <-time.After(5 * time.Second):
		d.t.Log("ccmux TUI did not exit after Ctrl-C within 5s")
	}
}
