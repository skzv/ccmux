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

// buildFakeClaude compiles a native program into dir/claude that prints
// the file named by its first argument and then sleeps for its second
// argument's seconds: a stand-in for Claude Code at its input box that
// tmux reports as `claude` (a script would show as its interpreter).
func buildFakeClaude(t *testing.T, dir string) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	src := t.TempDir()
	prog := `package main

import (
	"os"
	"strconv"
	"time"
)

func main() {
	b, _ := os.ReadFile(os.Args[1])
	os.Stdout.Write(b)
	n, _ := strconv.ParseFloat(os.Args[2], 64)
	time.Sleep(time.Duration(n * float64(time.Second)))
}
`
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module fakeclaude\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "claude")
	cmd := exec.Command(goBin, "build", "-o", out, ".")
	cmd.Dir = src
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	return out
}

// TestPollOnce_HandRunAgentOnRealTmux — on a real tmux: `claude` run by
// hand in an interactive shell, in a plain tmux session and in a ccmux
// shell session (tagged "shell"), is classified as Claude while it runs
// (tmux reports it as the pane's foreground command) and the session is
// a shell again once it exits.
func TestPollOnce_HandRunAgentOnRealTmux(t *testing.T) {
	dir := pollSandbox(t)
	shell := "zsh -f"
	if _, err := exec.LookPath("zsh"); err != nil {
		shell = "bash --norc --noprofile -i"
	}
	fake := buildFakeClaude(t, t.TempDir())
	fixture, err := filepath.Abs(filepath.Join("..", "..", "internal", "agent", "testdata", "panes", "claude_v2_idle.txt"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := tmux.New(ctx, "scratch", dir, shell); err != nil {
		t.Fatal(err)
	}
	if err := tmux.NewWithAgent(ctx, "c-shell-1", dir, shell, tmux.ShellAgentTag); err != nil {
		t.Fatal(err)
	}
	srv := newServer(testDaemonCfg(dir))
	srv.startSleepManager()
	poll := func() {
		srv.pollOnce(ctx, 50*time.Millisecond)
		time.Sleep(100 * time.Millisecond)
	}
	waitFor := func(name string, want agent.ID, st agent.State) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			poll()
			tr := srv.seen[name]
			if tr != nil && tr.agentID == want && tr.state == st {
				return
			}
			if time.Now().After(deadline) {
				if tr == nil {
					t.Fatalf("%s: not tracked", name)
				}
				t.Fatalf("%s: agent %q in state %s, want %q in %s", name, tr.agentID, tr.state, want, st)
			}
		}
	}
	for _, name := range []string{"scratch", "c-shell-1"} {
		waitFor(name, shellAgentID, agent.StateIdle)
		mustTmux(t, "send-keys", "-t", "="+name+":", "-l", "--", fake+" "+fixture+" 3")
		mustTmux(t, "send-keys", "-t", "="+name+":", "Enter")
		waitFor(name, agent.IDClaude, agent.StateNeedsInput)
		waitFor(name, shellAgentID, agent.StateIdle) // it exited
	}
}
