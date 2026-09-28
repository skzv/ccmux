//go:build integration

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// scriptedPanes fakes only the pane bodies of real tmux sessions: each
// pane (by its tmux id, which survives a rename) shows the body a test
// sets for it. Everything else — the session list, the panes, their
// titles, attached clients, the options the daemon writes — is tmux's.
type scriptedPanes struct {
	mu     sync.Mutex
	byPane map[string]string
}

func (sp *scriptedPanes) set(paneID, body string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.byPane[paneID] = body
}

func (sp *scriptedPanes) wire(srv *server) {
	srv.capturePane = func(_ context.Context, id string, _ int) (string, error) {
		sp.mu.Lock()
		defer sp.mu.Unlock()
		if b, ok := sp.byPane[id]; ok {
			return b, nil
		}
		return "", errors.New("can't find pane: " + id)
	}
	// Only reached when the session's panes can't be listed.
	srv.capture = func(context.Context, string, int) (string, error) {
		return "", errors.New("no pane list")
	}
}

// review is what the daemon holds of a session's prompt count and
// reviewed flag, as "prompts=N seen=B" ("untracked" when it has no
// entry for it).
func review(srv *server, name string) string {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	tr, ok := srv.seen[name]
	if !ok {
		return "untracked"
	}
	return fmt.Sprintf("prompts=%d seen=%v", tr.promptCount, tr.seen)
}

func tmuxOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// attachControlClient attaches a control-mode client to the session — a
// real attached client as far as #{session_attached} goes, with no
// terminal needed — and returns a function that detaches it.
func attachControlClient(t *testing.T, name string) (detach func()) {
	t.Helper()
	cmd := exec.Command("tmux", "-C", "attach", "-t", "="+name+":")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitAttached := func(want string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if tmuxOut(t, "display-message", "-p", "-t", "="+name+":", "#{session_attached}") == want {
				return
			}
		}
		t.Fatalf("session %s: attached never became %s", name, want)
	}
	waitAttached("1")
	var once sync.Once
	detach = func() {
		once.Do(func() {
			_ = stdin.Close() // control mode exits at end of input
			_ = cmd.Wait()
			waitAttached("0")
		})
	}
	t.Cleanup(detach)
	return detach
}

// TestPollOnce_ReviewSurvivesRestartOnRealTmux — on a real (isolated)
// tmux: a session whose agent ended a turn the user then looked at, and
// one sitting reviewed at its first prompt, come back from a daemon
// restart as they were — reviewed, with the prompt counted — and so
// does the first after `tmux rename-session` behind the daemon's back.
// Before, the restart brought both back unreviewed (they wait for input)
// with no prompts counted. Restarting is a fresh server struct over the
// same tmux server; only the pane bodies are scripted.
func TestPollOnce_ReviewSurvivesRestartOnRealTmux(t *testing.T) {
	dir := pollSandbox(t)
	idle, working := readFixture(t, "claude_v2_idle.txt"), readFixture(t, "claude_v2_working.txt")
	panes := &scriptedPanes{byPane: map[string]string{}}
	for _, name := range []string{"c-turn", "c-waiting"} {
		mustTmux(t, "new-session", "-d", "-s", name, "-c", dir, "sleep 300")
		mustTmux(t, "set-option", "-t", "="+name+":", "@ccmux_agent", "claude")
		panes.set(tmuxOut(t, "display-message", "-p", "-t", "="+name+":", "#{pane_id}"), idle)
	}
	turnPane := tmuxOut(t, "display-message", "-p", "-t", "=c-turn:", "#{pane_id}")
	title := func(s string) { mustTmux(t, "select-pane", "-t", "=c-turn:", "-T", s) }
	title("✳ Claude Code")

	srv := newServer(testDaemonCfg(dir))
	srv.startSleepManager()
	// The sessions were made while this daemon ran: news from the start.
	srv.startedAt = time.Now().Add(-time.Minute)
	panes.wire(srv)
	pollNTimes(srv, 2)
	title("⠋ Fix the flaky test")
	panes.set(turnPane, working)
	pollNTimes(srv, 1)
	title("⠙ Fix the flaky test")
	pollNTimes(srv, 1)
	title("✳ Fix the flaky test")
	panes.set(turnPane, idle)
	pollNTimes(srv, 3)
	if got := review(srv, "c-turn"); got != "prompts=1 seen=false" {
		t.Fatalf("setup: the turn's end: %s, want prompts=1 seen=false", got)
	}
	detach := attachControlClient(t, "c-turn") // the user looks
	pollNTimes(srv, 1)
	detach()
	pollNTimes(srv, 1)
	for name, want := range map[string]string{"c-turn": "1 1 needs_input", "c-waiting": "1 0 needs_input"} {
		got := tmuxOut(t, "display-message", "-p", "-t", "="+name+":", "#{@ccmux_seen} #{@ccmux_prompts} #{@ccmux_state}")
		if got != want {
			t.Fatalf("%s's record on the session: %q, want %q", name, got, want)
		}
	}

	// The daemon restarts.
	srv2 := newServer(testDaemonCfg(dir))
	srv2.startSleepManager()
	panes.wire(srv2)
	pushes := countPushes(t, srv2)
	bells := countBells(srv2)
	pollNTimes(srv2, 2)
	if got := review(srv2, "c-turn"); got != "prompts=1 seen=true" {
		t.Errorf("c-turn after a restart: %s, want prompts=1 seen=true", got)
	}
	if got := review(srv2, "c-waiting"); got != "prompts=0 seen=true" {
		t.Errorf("c-waiting after a restart: %s, want prompts=0 seen=true", got)
	}

	// Renamed behind the daemon's back.
	mustTmux(t, "rename-session", "-t", "=c-turn:", "c-renamed")
	pollNTimes(srv2, 2)
	if got := review(srv2, "c-renamed"); got != "prompts=1 seen=true" {
		t.Errorf("after a rename in tmux: %s, want prompts=1 seen=true", got)
	}
	if got := pushes(); got != 0 || *bells != 0 {
		t.Errorf("the restart or the rename notified: pushes=%d bells=%d", got, *bells)
	}
}
