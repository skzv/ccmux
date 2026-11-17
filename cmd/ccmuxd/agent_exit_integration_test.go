//go:build integration

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/tmux"
)

// fakeCrashingAgent is a native stand-in for an agent that sits at its
// prompt and then crashes: run as `<agent> resume …` (the launch chain's
// first try) it draws Codex's caret, waits its first argument's worth of
// seconds and dies with exit 1; run bare (the chain's retry) it fails at
// once. A native binary, so that its process is what ps sees — a
// script would show as its interpreter, a shell.
const fakeCrashingAgent = `package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "resume" {
		fmt.Println("error: no session to start")
		os.Exit(1)
	}
	fmt.Print("\033]2;codex\007")
	fmt.Print("OpenAI Codex (fake)\n\n> ")
	time.Sleep(3 * time.Second)
	fmt.Println("\nthread 'main' panicked at src/main.rs:1:1")
	os.Exit(1)
}
`

func buildFakeAgent(t *testing.T, out string) {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(fakeCrashingAgent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module fakeagent\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(goBin, "build", "-o", out, ".")
	cmd.Dir = src
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
}

// TestPollOnce_AgentCrashToShellOnRealTmux — on a real tmux, through
// the launch chain ccmux starts a Codex project session with: the agent
// waits at its prompt, crashes, the chain's retry fails too, and the
// chain falls back to an interactive shell. The pane goes quiet under a
// shell prompt Codex's rules don't know, which read as waiting for input
// — a bell and a push for a crash. The pane's process tree has only
// shells left in it: error, with no bell and no push.
func TestPollOnce_AgentCrashToShellOnRealTmux(t *testing.T) {
	dir := pollSandbox(t)
	// An interactive zsh with no startup files offers its new-user menu;
	// an empty ~/.zshrc gives the plain prompt a crashed session shows.
	if err := os.WriteFile(filepath.Join(dir, ".zshrc"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "codex")
	buildFakeAgent(t, fake)

	cfg := testDaemonCfg(dir)
	cfg.Notifications.Bell = true
	srv := newServer(cfg)
	srv.startSleepManager()
	srv.startedAt = time.Now().Add(-time.Hour) // the session is created while it watches
	bells := 0
	srv.bell = func(context.Context, string) error { bells++; return nil }
	pushes := countPushes(t, srv)

	ctx := context.Background()
	launch := agent.LaunchCmd(agent.IDCodex, true, agent.Commands{Codex: fake})
	if err := tmux.New(ctx, "c-crash", dir, launch); err != nil {
		t.Fatal(err)
	}
	mustTmux(t, "set-option", "-t", "=c-crash:", "@ccmux_agent", "codex")

	const idle = 600 * time.Millisecond
	var states []agent.State
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		srv.pollOnce(ctx, idle)
		if tr := srv.seen["c-crash"]; tr != nil {
			if n := len(states); n == 0 || states[n-1] != tr.state {
				states = append(states, tr.state)
			}
			if tr.state == agent.StateError && time.Since(tr.lastChange) > 3*idle {
				break // settled in error well past the idle threshold
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("states: %v", states)
	tr := srv.seen["c-crash"]
	if tr == nil || tr.state != agent.StateError {
		t.Fatalf("after the crash: states %v, want error at the end", states)
	}
	sawIdle := false
	for _, st := range states {
		if st == agent.StateIdle {
			sawIdle = true
		}
		if st == agent.StateNeedsInput {
			t.Errorf("states %v: the crash read as waiting for input", states)
		}
	}
	if !sawIdle {
		t.Errorf("states %v: never saw the live agent at its caret (idle)", states)
	}
	if bells != 0 || pushes() != 0 || tr.promptCount != 0 {
		t.Errorf("the crash notified: bells=%d pushes=%d promptCount=%d, want 0", bells, pushes(), tr.promptCount)
	}
}
