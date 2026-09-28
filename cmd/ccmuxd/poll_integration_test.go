//go:build integration

package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tmux"
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

// fakePaneBody makes every pane read return body() — both the read of
// the agent's pane by id and the session-target fallback — while the
// session and its panes stay real.
func fakePaneBody(srv *server, body func() (string, error)) {
	srv.capture = func(context.Context, string, int) (string, error) { return body() }
	srv.capturePane = func(context.Context, string, int) (string, error) { return body() }
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
	fakePaneBody(srv, func() (string, error) { return needsInputPane, nil })
	var bells int
	srv.bell = func(context.Context, string) error { bells++; return nil }

	// Pre-seed: same content already recorded, lastChange in the past,
	// prior state active after real work — so this poll classifies
	// needs_input and counts it as the end of a turn.
	srv.seen["c-bell"] = &tracked{
		last:       needsInputPane,
		lastChange: time.Now().Add(-time.Hour),
		state:      agent.StateActive,
		agentID:    agent.IDClaude,
		pollTrack:  pollTrack{turn: turn{worked: true}},
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

// TestPollOnce_AgentTagWinsOverResolvedAgent — on a real tmux: a session
// the daemon resolved from its project's sidecar keeps that agent when
// the sidecar changes (switching a project's agent applies to its next
// session, not the ones running), but the session's own @ccmux_agent
// tag always wins: a resumed Muse thread keeps its own classifier even
// though the workspace defaults to Claude (and may have Claude sessions
// open).
func TestPollOnce_AgentTagWinsOverResolvedAgent(t *testing.T) {
	dir := pollSandbox(t)
	mustTmux(t, "new-session", "-d", "-s", "c-agent", "-c", dir)

	srv := newServer(testDaemonCfg(dir))
	srv.startSleepManager()
	fakePaneBody(srv, func() (string, error) { return "stable pane output", nil })
	sidecar := agent.IDClaude
	srv.readAgent = func(string) agent.ID { return sidecar }

	srv.pollOnce(context.Background(), time.Second)
	if got := srv.seen["c-agent"].agentID; got != agent.IDClaude {
		t.Fatalf("agentID = %q, want %q from the sidecar", got, agent.IDClaude)
	}
	sidecar = agent.IDCursor // the project is switched to another agent
	srv.pollOnce(context.Background(), time.Second)
	if got := srv.seen["c-agent"].agentID; got != agent.IDClaude {
		t.Fatalf("agentID = %q after the project's agent changed, want the running %q", got, agent.IDClaude)
	}

	mustTmux(t, "set-option", "-t", "c-agent", "@ccmux_agent", "muse")
	srv.pollOnce(context.Background(), time.Second)
	if got := srv.seen["c-agent"].agentID; got != agent.IDMuse {
		t.Fatalf("explicit resumed agent lost: %q", got)
	}
	mustTmux(t, "set-option", "-t", "c-agent", "@ccmux_agent", "codex")
	srv.pollOnce(context.Background(), time.Second)
	if got := srv.seen["c-agent"].agentID; got != agent.IDCodex {
		t.Fatalf("a changed tag must be followed: agentID = %q, want %q", got, agent.IDCodex)
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
	fakePaneBody(srv, func() (string, error) { return "", errors.New("simulated capture failure") })

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

// TestPollOnce_MultiWindowSessionReadsAgentPane — on a real tmux: the
// agent runs in window 0 and the user opened a shell window, which is
// now the session's active pane. The daemon read that shell and showed
// the session as a crashed agent (error); it must classify the agent's
// pane.
func TestPollOnce_MultiWindowSessionReadsAgentPane(t *testing.T) {
	dir := pollSandbox(t)
	fixture, err := filepath.Abs(filepath.Join("..", "..", "internal", "agent", "testdata", "panes", "claude_v2_idle.txt"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := tmux.New(ctx, "c-multi", dir, "cat '"+fixture+"'; exec sleep 300"); err != nil {
		t.Fatal(err)
	}
	mustTmux(t, "new-window", "-t", "=c-multi:", "-c", dir, `sh -c 'printf "user@host ~ %% "; exec sleep 300'`)

	srv := newServer(testDaemonCfg(dir))
	srv.startSleepManager()
	for i := 0; i < 4; i++ {
		srv.pollOnce(ctx, 50*time.Millisecond)
		time.Sleep(100 * time.Millisecond)
	}
	tr := srv.seen["c-multi"]
	if tr == nil {
		t.Fatal("session not tracked")
	}
	if tr.state != agent.StateNeedsInput {
		t.Errorf("state = %s, want needs_input from the agent's pane (the active shell window reads as error)", tr.state)
	}
}

// TestPollOnce_CrashedAgentSpinnerGoesStale — on a real tmux: a fake
// agent sets a working-spinner title, prints a line and dies (exit 134),
// and its launch chain falls back to a shell. tmux keeps the dead
// agent's #{pane_title}, which pinned the session "active" — and the
// sleep lock on — forever. Once nothing in the pane moves, the spinner
// must stop counting and the shell prompt must show as a crash.
func TestPollOnce_CrashedAgentSpinnerGoesStale(t *testing.T) {
	dir := pollSandbox(t)
	prev := spinnerStaleFloor
	spinnerStaleFloor = 0 // with a 50ms idle threshold: stale after 150ms
	t.Cleanup(func() { spinnerStaleFloor = prev })
	ctx := context.Background()
	fake := `printf '\033]2;⠋ Refactor poll loop\007'; echo 'Refactoring the poll loop'; sh -c 'exit 134' || exec sh -c 'printf "user@host ~ %% "; exec sleep 300'`
	if err := tmux.New(ctx, "c-crash", dir, fake); err != nil {
		t.Fatal(err)
	}

	srv := newServer(testDaemonCfg(dir))
	srv.startSleepManager()
	var title string
	for i := 0; i < 8; i++ {
		srv.pollOnce(ctx, 50*time.Millisecond)
		time.Sleep(100 * time.Millisecond)
		title = srv.seen["c-crash"].pane.title
	}
	if !strings.HasPrefix(title, "⠋") {
		t.Fatalf("setup: pane title = %q; want the dead agent's spinner still set", title)
	}
	if st := srv.seen["c-crash"].state; st != agent.StateError {
		t.Errorf("state = %s, want error: the spinner belongs to a dead agent", st)
	}
}

// TestPollOnce_ResizeWindowDoesNotRenotify — on a real tmux: `tmux
// resize-window` on a detached session that sits waiting for input.
// tmux reflows the pane and the program repaints on SIGWINCH; that
// redraw read as activity and brought a second needs-input bell, push
// and prompt count a few seconds later.
func TestPollOnce_ResizeWindowDoesNotRenotify(t *testing.T) {
	dir := pollSandbox(t)
	fixture, err := filepath.Abs(filepath.Join("..", "..", "internal", "agent", "testdata", "panes", "claude_v2_idle.txt"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Repaint on SIGWINCH the way a TUI does.
	script := `f='` + fixture + `'; trap 'printf "\033[H\033[2J"; cat "$f"' WINCH; cat "$f"; while :; do sleep 0.1; done`
	if err := tmux.New(ctx, "c-resize", dir, script); err != nil {
		t.Fatal(err)
	}
	cfg := testDaemonCfg(dir)
	cfg.Notifications.Bell = true
	srv := newServer(cfg)
	srv.startSleepManager()
	bells := 0
	srv.bell = func(context.Context, string) error { bells++; return nil }
	poll := func(n int) {
		for i := 0; i < n; i++ {
			srv.pollOnce(ctx, 50*time.Millisecond)
			time.Sleep(300 * time.Millisecond)
		}
	}
	poll(4)
	tr := srv.seen["c-resize"]
	if tr == nil || tr.state != agent.StateNeedsInput {
		t.Fatalf("setup: session not waiting for input: %+v", tr)
	}
	before, prompts, rang := tr.last, tr.promptCount, bells

	mustTmux(t, "resize-window", "-t", "=c-resize:", "-x", "100", "-y", "30")
	poll(5)

	if tr.last == before {
		t.Fatal("setup: the resize didn't change the captured pane")
	}
	if tr.state != agent.StateNeedsInput || tr.promptCount != prompts || bells != rang {
		t.Errorf("after resize-window: state=%s promptCount=%d bells=%d, want needs_input/%d/%d",
			tr.state, tr.promptCount, bells, prompts, rang)
	}
}
