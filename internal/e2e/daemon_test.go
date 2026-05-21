//go:build integration

package e2e

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestDaemonIPC_SocketMatchesTmux covers the Unix-socket IPC CUJ: a
// client querying the local daemon over its socket sees the same
// sessions and projects the harness observes directly in tmux.
func TestDaemonIPC_SocketMatchesTmux(t *testing.T) {
	e := newEnv(t)
	writeFile(t, filepath.Join(e.Root, "ipcproj", "CLAUDE.md"), "# ipcproj\n")
	e.newTmuxSession("c-ipc-sess", e.Root)
	e.startDaemon()

	cli := e.localClient()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	h, err := cli.Health(ctx)
	if err != nil {
		t.Fatalf("Health over socket: %v", err)
	}
	if !h.OK {
		t.Errorf("Health.OK = false")
	}

	sessions, err := cli.Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions over socket: %v", err)
	}
	if !containsSession(sessions, "c-ipc-sess") {
		t.Errorf("daemon did not report c-ipc-sess; sessions = %v", sessionNamesOf(sessions))
	}

	projects, err := cli.Projects(ctx)
	if err != nil {
		t.Fatalf("Projects over socket: %v", err)
	}
	found := false
	for _, p := range projects {
		if p.Name == "ipcproj" {
			found = true
		}
	}
	if !found {
		t.Errorf("daemon did not report project ipcproj")
	}
}
