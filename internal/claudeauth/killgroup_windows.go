//go:build windows

package claudeauth

import "os/exec"

func killGroupOnCancel(*exec.Cmd) {}
