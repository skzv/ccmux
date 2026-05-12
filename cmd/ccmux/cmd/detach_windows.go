//go:build windows

package cmd

import (
	"os/exec"
	"syscall"
)

// detachProcess starts ccmuxd as a detached Windows process so it
// doesn't die when the parent shell does. CREATE_NEW_PROCESS_GROUP
// (0x00000200) plus DETACHED_PROCESS (0x00000008) is the Windows
// equivalent of Setsid. HideWindow keeps the daemon's console window
// from flashing up.
//
// Note: this path is untested on a real Windows box yet — flagged in
// docs/04_Guides/Windows.md as a TODO when the user provides a VPS.
func detachProcess(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
}
