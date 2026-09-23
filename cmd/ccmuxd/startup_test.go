package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

// TestRun_LosingDaemonStartsNothing — a second ccmuxd that finds a live
// peer on the socket must yield without starting any side effect. It
// used to start the sleep manager first, which reverted the live
// daemon's very_dangerous sleep override, and ran a poll tick (bells,
// pushes) before exiting.
func TestRun_LosingDaemonStartsNothing(t *testing.T) {
	// Short HOME: unix socket paths are capped at ~104 bytes on macOS.
	home, err := os.MkdirTemp("/tmp", "ccmuxd-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)

	sock, err := daemon.SocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil {
		t.Fatal(err)
	}
	peer, err := net.Listen("unix", sock) // the live daemon
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	go func() {
		for {
			c, err := peer.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	started := false
	orig := startBackground
	t.Cleanup(func() { startBackground = orig })
	startBackground = func(*server, context.Context) func() {
		started = true
		return func() {}
	}

	err = run()
	if !errors.Is(err, errPeerAlreadyServing) {
		t.Fatalf("run() = %v, want errPeerAlreadyServing", err)
	}
	if started {
		t.Error("a daemon that lost the bind race started its background work")
	}
}

// TestEnsureClipboard_ReappliesPerTmuxServer — the clipboard options
// live in the tmux server; ccmuxd usually starts before one exists, and
// a restarted server loses them. Apply once per server appearance.
func TestEnsureClipboard_ReappliesPerTmuxServer(t *testing.T) {
	s := newPollTestServer(t)
	applied := 0
	s.enableClipboard = func(context.Context) error { applied++; return nil }
	sessions := []tmux.Session(nil)
	var listErr error = errors.New("no server running")
	s.list = func(context.Context) ([]tmux.Session, error) { return sessions, listErr }
	s.capture = func(context.Context, string, int) (string, error) { return "", nil }
	tick := func() { s.pollOnce(context.Background(), time.Second) }

	tick() // no tmux server yet
	if applied != 0 {
		t.Fatalf("applied with no tmux server: %d", applied)
	}
	sessions, listErr = []tmux.Session{{Name: "c-a", Path: "/tmp"}}, nil
	tick()
	tick()
	if applied != 1 {
		t.Fatalf("server appeared: applied %d times, want exactly 1", applied)
	}
	sessions, listErr = nil, errors.New("no server running") // tmux exited
	tick()
	sessions, listErr = []tmux.Session{{Name: "c-b", Path: "/tmp"}}, nil
	tick()
	if applied != 2 {
		t.Errorf("new tmux server: applied %d times total, want 2", applied)
	}
}
