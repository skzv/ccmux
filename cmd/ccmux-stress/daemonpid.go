package main

import (
	"fmt"
	"os"

	"github.com/skzv/ccmux/internal/daemon"
)

// findCcmuxd returns the pid of the ccmuxd this run talks to: the
// process on the other end of the socket daemon.LocalClient dials
// ($HOME/.local/state/ccmux/ccmuxd.sock), read from the socket's peer
// credentials. It used to take the first `pgrep -x ccmuxd` match — any
// user's daemon, or a sandbox's — so RSS/FD numbers could describe a
// process the load never touched. A daemon that isn't ours (another
// uid) or can't be identified is an error, never a guess.
func findCcmuxd() (int, error) {
	sock, err := daemon.SocketPath()
	if err != nil {
		return 0, fmt.Errorf("locate the ccmuxd socket: %w", err)
	}
	pid, uid, err := socketPeer(sock)
	if err != nil {
		return 0, fmt.Errorf("can't identify the ccmuxd behind %s (is it running for this $HOME?): %w", sock, err)
	}
	if me := os.Getuid(); uid != me {
		return 0, fmt.Errorf("the process behind %s (pid %d) runs as uid %d, not %d — refusing to measure someone else's daemon", sock, pid, uid, me)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("the ccmuxd socket %s reported no peer pid", sock)
	}
	return pid, nil
}
