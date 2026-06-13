package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shortSock returns a socket path short enough for the OS sun_path
// limit (~104 chars on macOS) — t.TempDir() lives under a long
// /var/folders/... path that overflows it.
func shortSock(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// TestWaitForSocketHandoff_NoDaemon — a cold start (no socket) returns
// true immediately so startup adds no latency.
func TestWaitForSocketHandoff_NoDaemon(t *testing.T) {
	start := time.Now()
	if !waitForSocketHandoff(shortSock(t), 3*time.Second) {
		t.Fatal("no daemon present — should be safe to bind (true)")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("cold start should return ~instantly, took %v", elapsed)
	}
}

// TestWaitForSocketHandoff_PersistentPeer — a daemon that keeps serving
// past the window is detected: returns false so the new instance yields
// cleanly (no respawn loop), exactly as before.
func TestWaitForSocketHandoff_PersistentPeer(t *testing.T) {
	sock := shortSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go acceptForever(ln)

	if waitForSocketHandoff(sock, 500*time.Millisecond) {
		t.Error("a persistently-serving peer should NOT be handed off (want false)")
	}
}

// TestWaitForSocketHandoff_RestartHandoff — the fix. A peer that closes
// its listener mid-window (the previous daemon finishing its graceful
// shutdown) lets the new instance bind: returns true once the socket
// frees, well within the timeout — instead of yielding and leaving the
// daemon down until launchd's respawn throttle.
func TestWaitForSocketHandoff_RestartHandoff(t *testing.T) {
	sock := shortSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	go acceptForever(ln)
	// Simulate the old daemon finishing shutdown shortly after we start.
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = ln.Close()
	}()

	start := time.Now()
	if !waitForSocketHandoff(sock, 3*time.Second) {
		t.Fatal("peer closed mid-window — handoff should succeed (true)")
	}
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Errorf("handoff should complete shortly after the peer closes, took %v", elapsed)
	}
}

func acceptForever(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_ = c.Close()
	}
}
