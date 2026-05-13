package main

import (
	"net"
	"os"
	"time"
)

// isAnotherDaemonAlive returns true iff some process is currently
// listening on the Unix socket at sockPath. It's the gate that keeps
// two ccmuxd processes from coexisting after the startup race documented
// in main.go's bind block.
//
// Detection rules, in order:
//
//  1. If no file exists at sockPath, no one's listening. Return false.
//  2. If a file exists but isn't a socket (e.g. a stray regular file
//     left behind by something weird), treat it as a stale artifact —
//     callers will os.Remove + re-bind. Return false.
//  3. If a socket file exists, try to dial it with a short timeout.
//     Dial success → a live daemon is on the other end. Return true.
//     Dial failure (connection refused, etc) → the socket file is
//     stale (previous crash, kill -9, …). Return false.
//
// Short timeout is deliberate — a live daemon's accept loop replies
// immediately. A long timeout would hang startup behind a wedged
// peer. 300ms is safe headroom over any local Unix-socket dial.
func isAnotherDaemonAlive(sockPath string, dialTimeout time.Duration) bool {
	fi, err := os.Lstat(sockPath)
	if err != nil {
		// No file → nothing to compete with.
		return false
	}
	if fi.Mode()&os.ModeSocket == 0 {
		// Exists but isn't a socket — let the caller clean it up.
		return false
	}
	conn, err := net.DialTimeout("unix", sockPath, dialTimeout)
	if err != nil {
		// Socket file present but nothing accepting — stale, safe to remove.
		return false
	}
	_ = conn.Close()
	return true
}
