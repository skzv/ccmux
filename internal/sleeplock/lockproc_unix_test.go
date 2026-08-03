//go:build unix

package sleeplock

import (
	"os/exec"
	"testing"
)

// TestStartLockProcFor_LinuxHolderGetsOwnProcessGroup — regression for
// the orphaned `sleep infinity` leak: releaseLocked killed only the
// systemd-inhibit parent, leaving its sleep child behind on every
// engage/release cycle. The linux holder must start with Setpgid so
// the release path can kill the whole group.
//
// Unix-tagged: syscall.SysProcAttr's fields are platform-specific, so
// this assertion can't compile on windows.
func TestStartLockProcFor_LinuxHolderGetsOwnProcessGroup(t *testing.T) {
	for _, mode := range []Mode{ModeSafe, ModeDangerous, ModeVeryDangerous} {
		cmd := startLockProcFor("linux", mode)
		if cmd == nil {
			t.Fatalf("startLockProcFor(linux, %s) = nil", mode)
		}
		if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
			t.Errorf("mode %s: linux holder must have SysProcAttr.Setpgid=true, got %+v", mode, cmd.SysProcAttr)
		}
	}
	// The darwin holder relies on `caffeinate -w` instead — no Setpgid.
	if cmd := startLockProcFor("darwin", ModeSafe); cmd.SysProcAttr != nil {
		t.Errorf("darwin holder should not set SysProcAttr, got %+v", cmd.SysProcAttr)
	}
}

// TestKillProcessGroup_FallsBackWithoutSetpgid — killProcessGroup must
// decline (return false) for holders not started in their own group, so
// the release path falls back to plain Process.Kill.
func TestKillProcessGroup_FallsBackWithoutSetpgid(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if killProcessGroup(cmd) {
		t.Error("killProcessGroup must return false when Setpgid was not requested")
	}
	// Not started yet → also decline even when Setpgid is set.
	grp := exec.Command("sleep", "60")
	setNewProcessGroup(grp)
	if killProcessGroup(grp) {
		t.Error("killProcessGroup must return false for a never-started command")
	}
}
