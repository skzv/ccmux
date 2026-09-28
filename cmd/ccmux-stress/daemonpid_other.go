//go:build !darwin && !linux

package main

import (
	"fmt"
	"runtime"
)

// socketPeer isn't implemented here: ccmux-stress measures a local
// ccmuxd on macOS and Linux only.
func socketPeer(string) (pid, uid int, err error) {
	return 0, 0, fmt.Errorf("reading a unix socket's peer isn't supported on %s", runtime.GOOS)
}
