//go:build !windows

package cmd

import (
	"os/exec"
	"syscall"
)

// detachProcess puts the spawned ccmuxd into its own process group so
// it survives the parent (the `ccmux daemon start` invocation) exiting.
// Setsid is the canonical Unix trick — without it the daemon dies the
// moment the user's shell does.
func detachProcess(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
