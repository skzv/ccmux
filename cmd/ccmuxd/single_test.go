package main

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// tempSocketPath returns a short-enough path under /tmp to fit inside
// macOS's 104-byte sockaddr_un limit. The default t.TempDir() lives
// under /var/folders/.../<sometest>/T which overflows.
func tempSocketPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ccmuxd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

// TestIsAnotherDaemonAlive_NoFile — the empty-slate case. No socket
// file means no daemon to compete with, so the new daemon proceeds.
// This is the "fresh boot" path.
func TestIsAnotherDaemonAlive_NoFile(t *testing.T) {
	sock := tempSocketPath(t, "absent.sock")
	if isAnotherDaemonAlive(sock, 100*time.Millisecond) {
		t.Errorf("missing socket file should not be flagged as a live peer")
	}
}

// TestIsAnotherDaemonAlive_StaleSocketFile — the post-crash case. A
// socket file exists but no process is bound to it (this happens when
// ccmuxd is `kill -9`'d without a chance to clean up). The new daemon
// should be allowed to remove it and bind a fresh one; if we refused
// here a single kill -9 would brick the user's setup.
func TestIsAnotherDaemonAlive_StaleSocketFile(t *testing.T) {
	sock := tempSocketPath(t, "stale.sock")
	// Bind, close, leave the file behind.
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	// On macOS net.Listen("unix") removes the file on close; recreate
	// a regular file at the path so the next branch (mode check) sees
	// "exists but not a socket". This models the "leftover file from
	// some other tool" case.
	if _, err := os.Stat(sock); os.IsNotExist(err) {
		if err := os.WriteFile(sock, []byte{}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if isAnotherDaemonAlive(sock, 100*time.Millisecond) {
		t.Errorf("stale file at sock path should not be flagged as live")
	}
}

// TestIsAnotherDaemonAlive_LiveListener — the case we're actually
// protecting against. A live listener is on the socket; the second
// daemon must back off rather than start up alongside it. Without
// this gate the orphaned-daemon RSS-bloat bug reproduces every time
// the user races `ccmux daemon start` against a launchd-managed
// daemon.
func TestIsAnotherDaemonAlive_LiveListener(t *testing.T) {
	sock := tempSocketPath(t, "live.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// Serve trivially so dial completes the handshake.
	srv := &http.Server{}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	if !isAnotherDaemonAlive(sock, 500*time.Millisecond) {
		t.Errorf("live listener at sock path should be detected")
	}
}

// TestIsAnotherDaemonAlive_NonSocketPath — defensive: a regular file
// at the path (created by a user mistake, or some other tool clobber)
// must not be treated as a live daemon. Treat it as a stale artifact
// so the daemon can remove + bind through it.
func TestIsAnotherDaemonAlive_NonSocketPath(t *testing.T) {
	sock := tempSocketPath(t, "regular.txt")
	if err := os.WriteFile(sock, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if isAnotherDaemonAlive(sock, 100*time.Millisecond) {
		t.Errorf("regular file at sock path should not be flagged as live")
	}
}

// TestIsAnotherDaemonAlive_HonorsShortTimeout — the dial timeout must
// be short so a wedged peer can't block daemon startup indefinitely.
// We simulate a wedged peer with a Unix listener that accepts but
// never replies; the dial should succeed (because accept does) which
// is the expected behavior here (we DO want to flag this as alive).
// Real point of this test: the call returns quickly, not after some
// multi-second default.
func TestIsAnotherDaemonAlive_HonorsShortTimeout(t *testing.T) {
	sock := tempSocketPath(t, "slow.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// Accept connections but don't read or write — simulates a wedged
	// daemon. Dial succeeds and isAnotherDaemonAlive returns true; the
	// returned conn is closed.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c // hold and drop
		}
	}()

	start := time.Now()
	got := isAnotherDaemonAlive(sock, 200*time.Millisecond)
	elapsed := time.Since(start)
	if !got {
		t.Errorf("wedged-accept listener should still be flagged as live")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("call took %v, expected fast return (~ms) even with wedged peer", elapsed)
	}
}
