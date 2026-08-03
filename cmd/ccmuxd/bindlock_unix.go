//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// bindLock is an exclusive advisory flock on a sidecar file next to
// the daemon's Unix socket. It serializes the probe→remove→listen
// bind sequence in run(): without it, two daemons racing the handoff
// window can both probe the socket as "free", and the loser's
// os.Remove unlinks the winner's freshly-bound socket — leaving a
// reachable-by-nobody rogue daemon (the leak the bind comments in
// main.go describe). The lock file itself is never removed; a stale
// file with no holder is harmless and instantly lockable.
type bindLock struct{ f *os.File }

// acquireBindLock takes the exclusive lock on path, retrying a
// non-blocking flock until timeout. Non-blocking-with-deadline rather
// than a bare blocking flock so a wedged holder can't hang startup
// forever — after the window we yield like any other "peer already
// serving" case.
func acquireBindLock(path string, timeout time.Duration) (*bindLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open bind lock %q: %w", path, err)
	}
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &bindLock{f: f}, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = f.Close()
			return nil, fmt.Errorf("flock %q: %w", path, err)
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("bind lock %q held by another process", path)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// release drops the lock. Safe on a nil receiver so error paths can
// call it unconditionally.
func (l *bindLock) release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	l.f = nil
}
