//go:build windows

package claudemodels

import "os/exec"

// killGroupOnCancel keeps exec.CommandContext's default (kill the
// process itself) on Windows, which has no process groups to signal;
// cmd.WaitDelay still bounds a child left holding the pipes.
func killGroupOnCancel(*exec.Cmd) {}
