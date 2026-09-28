//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

// TestSendKeys_RealTmuxLandsInAgentWindow — on a real tmux: the agent
// runs in window 0 and the user opened a shell window, which is now the
// session's active pane. POST /send-keys used to type into that shell,
// where a reply meant for the agent runs as a command. The keys must
// land in window 0, and /preview must read it too.
func TestSendKeys_RealTmuxLandsInAgentWindow(t *testing.T) {
	dir := pollSandbox(t)
	ctx := context.Background()
	// A stand-in agent that echoes whatever it is sent.
	if err := tmux.New(ctx, "c-keys", dir, "exec cat"); err != nil {
		t.Fatal(err)
	}
	mustTmux(t, "new-window", "-t", "=c-keys:", "-c", dir, "exec sh")
	if active, err := exec.Command("tmux", "display-message", "-p", "-t", "=c-keys:", "#{window_index}").Output(); err != nil || strings.TrimSpace(string(active)) != "1" {
		t.Fatalf("setup: the shell window should be active, got %q (%v)", active, err)
	}

	httpSrv := newAPIServer(t, dir)
	ran := filepath.Join(dir, "ran-in-the-shell")
	for _, keys := range []string{"touch " + ran, "Enter"} {
		if code, body := postJSON(t, httpSrv, "/v1/sessions/c-keys/send-keys", daemon.SendKeysRequest{Keys: keys}); code != http.StatusNoContent {
			t.Fatalf("send-keys %q: status %d (%s)", keys, code, body)
		}
	}

	capture := func(target string) string {
		out, _ := exec.Command("tmux", "capture-pane", "-p", "-t", target).Output()
		return string(out)
	}
	var agentPane string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if agentPane = capture("=c-keys:0"); strings.Count(agentPane, "touch "+ran) >= 2 { // tty echo + cat
			break
		}
	}
	if !strings.Contains(agentPane, "touch "+ran) {
		t.Errorf("the agent's window never got the keys:\n%s", agentPane)
	}
	time.Sleep(200 * time.Millisecond) // give a misdirected command time to run
	if _, err := os.Stat(ran); err == nil {
		t.Error("the keys ran as a command in the active shell window")
	}
	if shell := capture("=c-keys:1"); strings.Contains(shell, "touch") {
		t.Errorf("the active shell window got the keys:\n%s", shell)
	}

	resp, err := httpSrv.Client().Get(httpSrv.URL + "/v1/sessions/c-keys/preview?lines=10")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var prev daemon.PreviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&prev); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prev.Content, "touch "+ran) {
		t.Errorf("preview = %q, want the agent's window", prev.Content)
	}
}
