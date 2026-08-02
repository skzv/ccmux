//go:build unix

package sleeplock

import (
	"os/exec"
	"syscall"
)

// setNewProcessGroup asks the OS to start cmd in its own process group
// so the release path can kill the whole group at once. Used for the
// linux holder (systemd-inhibit forks a `sleep infinity` child; killing
// only the parent orphaned one sleeper per engage/release cycle).
func setNewProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup kills cmd's entire process group when it was started
// with Setpgid. Returns false when group kill doesn't apply (no
// Setpgid, not started) so the caller can fall back to killing just the
// process.
func killProcessGroup(cmd *exec.Cmd) bool {
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid || cmd.Process == nil {
		return false
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	return true
}
