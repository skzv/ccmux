//go:build integration

package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
)

// pollSandbox sets up an isolated tmux server (via TMUX_TMPDIR) and a
// temp $HOME, and returns the sandbox directory. Used as the working
// directory for the poll-loop integration tests.
func pollSandbox(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed — skipping daemon poll integration test")
	}
	// /tmp keeps the tmux socket path short enough for sockaddr_un.
	dir, err := os.MkdirTemp("/tmp", "ccmd")
	if err != nil {
		t.Fatalf("sandbox dir: %v", err)
	}
	// Setenv first so the kill-server cleanup (registered after) still
	// sees TMUX_TMPDIR — t.Cleanup runs LIFO before env restoration.
	// Clear TMUX too: when tests are launched from inside tmux, the
	// inherited client variable takes precedence and targets the live
	// server even if TMUX_TMPDIR points at this sandbox.
	t.Setenv("HOME", dir)
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	return dir
}

func mustTmux(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
}

// testDaemonCfg is the baseline config for daemon integration tests.
// Sleep mode is forced off so pollOnce's SetActive call can never
// spawn a real caffeinate / systemd-inhibit process.
func testDaemonCfg(dir string) config.Config {
	cfg := config.Defaults()
	cfg.Projects.Root = dir
	cfg.Sleep.Mode = "off"
	cfg.Daemon.PollIntervalSeconds = 1
	cfg.Daemon.IdleSecondsForNeedsInput = 1
	return cfg
}

// TestPollOnce_DetectsAndClassifies covers the CUJ: one poll cycle
// detects a live tmux session and assigns it a valid classified state.
func TestPollOnce_DetectsAndClassifies(t *testing.T) {
	dir := pollSandbox(t)
	mustTmux(t, "new-session", "-d", "-s", "c-poll", "-c", dir)

	srv := newServer(testDaemonCfg(dir))
	srv.startSleepManager()
	srv.pollOnce(context.Background(), time.Second)

	tr, ok := srv.seen["c-poll"]
	if !ok {
		t.Fatal("pollOnce did not track session c-poll")
	}
	switch tr.state {
	case agent.StateUnknown, agent.StateActive, agent.StateIdle, agent.StateNeedsInput, agent.StateError:
		// any of the five canonical states is acceptable
	default:
		t.Errorf("session classified to invalid state %q", tr.state)
	}
}

// TestPollOnce_BellOnNeedsInput covers the bell CUJ: a session that
// transitions into needs_input rings the bell exactly once, and a
// subsequent poll with the same content does not re-ring.
func TestPollOnce_BellOnNeedsInput(t *testing.T) {
	dir := pollSandbox(t)
	mustTmux(t, "new-session", "-d", "-s", "c-bell", "-c", dir)

	cfg := testDaemonCfg(dir)
	cfg.Notifications.Bell = true
	srv := newServer(cfg)
	srv.startSleepManager()

	// A pane whose last non-empty line is Claude's box-drawing input
	// frame classifies as needs_input once the idle threshold is met.
	const needsInputPane = "doing work…\n╭─────────────╮\n│ > │\n╰─────────────╯"
	srv.capture = func(context.Context, string, int) (string, error) {
		return needsInputPane, nil
	}
	var bells int
	srv.bell = func(context.Context, string) error { bells++; return nil }

	// Pre-seed: same content already recorded, lastChange in the past,
	// prior state active — so this poll classifies needs_input and
	// counts it as a transition.
	srv.seen["c-bell"] = &tracked{
		last:       needsInputPane,
		lastChange: time.Now().Add(-time.Hour),
		state:      agent.StateActive,
		agentID:    agent.IDClaude,
	}

	srv.pollOnce(context.Background(), time.Second)
	if got := srv.seen["c-bell"].state; got != agent.StateNeedsInput {
		t.Fatalf("state = %q after poll, want needs_input", got)
	}
	if bells != 1 {
		t.Fatalf("bell rang %d times on the needs_input transition, want 1", bells)
	}

	// Second poll, same content, still needs_input — no re-ring.
	srv.pollOnce(context.Background(), time.Second)
	if bells != 1 {
		t.Errorf("bell rang again without a transition (total %d), want 1", bells)
	}
}

// TestPollOnce_RefreshesAgentID covers stale agent sidecars: if a
// session was first seen as one agent and the project metadata changes,
// the daemon must update the classifier before deciding whether to
// ring. Otherwise a stale fallback classifier can spam needs-input
// bells for a different running TUI.
func TestPollOnce_RefreshesAgentID(t *testing.T) {
	dir := pollSandbox(t)
	mustTmux(t, "new-session", "-d", "-s", "c-agent", "-c", dir)

	srv := newServer(testDaemonCfg(dir))
	srv.startSleepManager()
	srv.capture = func(context.Context, string, int) (string, error) {
		return "stable pane output", nil
	}
	srv.readAgent = func(string) agent.ID { return agent.IDClaude }
	srv.seen["c-agent"] = &tracked{
		last:        "stable pane output",
		lastChange:  time.Now().Add(-time.Hour),
		state:       agent.StateActive,
		agentID:     agent.IDCursor,
		projectPath: dir,
	}

	srv.pollOnce(context.Background(), time.Second)

	if got := srv.seen["c-agent"].agentID; got != agent.IDClaude {
		t.Fatalf("agentID = %q after poll, want refreshed %q", got, agent.IDClaude)
	}
}

// TestPollOnce_CaptureFailureSurfaced pins the fix for the silently
// swallowed capture error: a capture failure must be logged, and the
// session must keep its prior state rather than being dropped or
// blanked.
func TestPollOnce_CaptureFailureSurfaced(t *testing.T) {
	dir := pollSandbox(t)
	mustTmux(t, "new-session", "-d", "-s", "c-capfail", "-c", dir)

	srv := newServer(testDaemonCfg(dir))
	srv.startSleepManager()
	srv.seen["c-capfail"] = &tracked{
		last:       "previous content",
		lastChange: time.Now().Add(-time.Hour),
		state:      agent.StateIdle,
		agentID:    agent.IDClaude,
	}
	srv.capture = func(context.Context, string, int) (string, error) {
		return "", errors.New("simulated capture failure")
	}

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	srv.pollOnce(context.Background(), time.Second)

	if !strings.Contains(logBuf.String(), "capture-pane") {
		t.Errorf("capture failure was swallowed silently; log = %q", logBuf.String())
	}
	tr := srv.seen["c-capfail"]
	if tr == nil {
		t.Fatal("session dropped from tracking after a capture failure")
	}
	if tr.state != agent.StateIdle {
		t.Errorf("state = %q after a failed capture, want it left at idle", tr.state)
	}
}
