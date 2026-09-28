//go:build integration

package main

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// TestPollOnce_SpinnerMarkOnRealTmux — on a real (isolated) tmux: once
// the daemon has seen Codex's spinner title in a session it records that
// on the session itself (@ccmux_spinner), and a restarted daemon reads it
// back from the session list, so typing into Codex after the restart is
// not taken for a turn. Only the pane body is faked; the title is the
// pane's own, set with select-pane -T the way an agent sets it with OSC 2.
func TestPollOnce_SpinnerMarkOnRealTmux(t *testing.T) {
	dir := pollSandbox(t)
	mustTmux(t, "new-session", "-d", "-s", "c-spin", "-c", dir, "sleep 300")
	mustTmux(t, "set-option", "-t", "=c-spin:", "@ccmux_agent", "codex")
	title := func(s string) { mustTmux(t, "select-pane", "-t", "=c-spin:", "-T", s) }
	var mu sync.Mutex
	body := codexPane("")
	setBody := func(b string) {
		mu.Lock()
		defer mu.Unlock()
		body = b
	}
	readBody := func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		return body, nil
	}

	srv := newServer(testDaemonCfg(dir))
	srv.startSleepManager()
	fakePaneBody(srv, readBody)
	title("codex")
	pollNTimes(srv, 2)
	for _, frame := range []string{"⠋", "⠙", "⠹"} {
		title(frame + " codex")
		setBody(codexPane("> fix it\n\n• Working"))
		pollNTimes(srv, 1)
	}
	title("codex")
	setBody(codexPane("> fix it\n\n• Done."))
	pollNTimes(srv, 3)
	out, err := exec.Command("tmux", "show-options", "-v", "-t", "=c-spin:", "@ccmux_spinner").CombinedOutput()
	if got := strings.TrimSpace(string(out)); err != nil || got != "codex" {
		t.Fatalf("@ccmux_spinner = %q (err %v), want codex", got, err)
	}

	// The daemon restarts.
	srv2 := newServer(testDaemonCfg(dir))
	srv2.startSleepManager()
	fakePaneBody(srv2, readBody)
	pushes := countPushes(t, srv2)
	bells := countBells(srv2)
	pollNTimes(srv2, 2)
	tr := srv2.seen["c-spin"]
	if tr == nil || !tr.spinnerSeen {
		t.Fatalf("restarted daemon didn't read the mark back: %+v", tr)
	}
	setBody(codexPane("> fix it\n\n• Done.") + "and the docs")
	pollNTimes(srv2, 1) // typing
	pollNTimes(srv2, 3) // the pause
	if got := pushes(); got != 0 || *bells != 0 {
		t.Errorf("typing into Codex after a restart notified: pushes=%d bells=%d, want 0", got, *bells)
	}
}
