package tui

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/termsafe"
)

// stderrTailSize is how much of an attach command's stderr is kept for
// the failure toast: enough for tmux/ssh/mosh's last error lines.
const stderrTailSize = 4 << 10

// stderrTail keeps the last stderrTailSize bytes written to it.
type stderrTail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *stderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - stderrTailSize; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *stderrTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// captureStderr tees c's stderr to the terminal (the user still sees it
// while the command runs) and into a tail buffer the exit handler can
// report. Every attach failure used to toast "tmux: exit status 1" —
// tmux's actual complaint ("can't find session: …") went to the
// terminal and was wiped by the TUI's redraw.
func captureStderr(c *exec.Cmd) *stderrTail {
	tail := &stderrTail{}
	c.Stderr = io.MultiWriter(os.Stderr, tail)
	// The pipe the tee needs can be held open by a process the command
	// leaves behind (an ssh ControlMaster); don't let that hang the
	// return to the TUI.
	c.WaitDelay = time.Second
	return tail
}

// attachExit builds the attachExitedMsg for a finished attach/ssh/mosh
// command. label names the command in a failure toast. For an
// interactive shell (shell=true) the exit status is the remote shell's
// — only ssh's own 255 is a failure.
func attachExit(err error, label string, tail *stderrTail, rt *attachRemoteTarget, shell bool) attachExitedMsg {
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil // the command itself succeeded
	}
	var exitErr *exec.ExitError
	if shell && errors.As(err, &exitErr) && exitErr.ExitCode() != 255 {
		err = nil
	}
	msg := attachExitedMsg{Err: err, RemoteSSHTarget: rt, Command: label}
	if err != nil && tail != nil {
		msg.Stderr = tail.String()
	}
	return msg
}

// execAttach hands the terminal to c (tmux attach, ssh, mosh) and
// reports how it ended, stderr included.
func execAttach(c *exec.Cmd, label string, rt *attachRemoteTarget, shell bool) tea.Cmd {
	tail := captureStderr(c)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return attachExit(err, label, tail, rt, shell)
	})
}

// attachFailureText is the toast for a failed attach: the command that
// failed and what it said (its last stderr line), or its exit error
// when it said nothing.
func attachFailureText(msg attachExitedMsg) string {
	label := msg.Command
	if label == "" {
		label = "tmux"
	}
	detail := lastLine(termsafe.String(msg.Stderr))
	switch {
	case detail == "":
		detail = msg.Err.Error()
	case strings.Contains(detail, "can't find session") || strings.Contains(detail, "no sessions") ||
		strings.Contains(detail, "no server running"):
		detail = tr("the session no longer exists (killed outside ccmux?)") + " — " + detail
	}
	return label + ": " + detail
}

// lastLine is the last non-blank line of s, trimmed.
func lastLine(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}
