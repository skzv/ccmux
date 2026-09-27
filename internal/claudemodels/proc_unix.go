//go:build unix

package claudemodels

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// killGroupOnCancel starts cmd in its own process group and makes
// cancelling its context kill that whole group, not just cmd itself:
// `claude` is a Node program that spawns helpers, and one that
// outlived a timed-out `claude -p` held the output pipes open, so the
// fetch (and the daemon's shutdown, which waits for it) blocked long
// past the timeout. cmd must come from exec.CommandContext.
func killGroupOnCancel(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
