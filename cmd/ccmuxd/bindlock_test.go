//go:build !windows

package main

import (
	"path/filepath"
	"testing"
	"time"
)

// TestBindLock_ExcludesSecondAcquirer — finding 7: two daemons racing
// the socket handoff probe could both see "free" and the loser's
// os.Remove unlinked the winner's live socket. The sidecar flock
// serializes the sequence; while one holder has it, a second acquire
// must fail (after its timeout), and release must make the lock
// acquirable again.
func TestBindLock_ExcludesSecondAcquirer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ccmuxd.sock.lock")

	l1, err := acquireBindLock(path, time.Second)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if l2, err := acquireBindLock(path, 200*time.Millisecond); err == nil {
		l2.release()
		t.Fatal("second acquire succeeded while the first still held the lock")
	}

	l1.release()
	l3, err := acquireBindLock(path, time.Second)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	l3.release()
}

// TestBindLock_ReleaseIsNilSafe — error paths call release
// unconditionally; a nil guard must be a no-op, not a panic.
func TestBindLock_ReleaseIsNilSafe(t *testing.T) {
	var l *bindLock
	l.release() // must not panic
}
