//go:build windows

package sleeplock

import "os/exec"

// setNewProcessGroup is a no-op on Windows — sleeplock has no Windows
// holder (startLockProcFor returns nil), this stub just keeps the
// package cross-compiling.
func setNewProcessGroup(*exec.Cmd) {}

// killProcessGroup always defers to the plain Process.Kill fallback on
// Windows.
func killProcessGroup(*exec.Cmd) bool { return false }
