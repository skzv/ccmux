//go:build integration

package tmux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// buildSleeper compiles a tiny native program that sleeps for its first
// argument's seconds, into dir under each of names. A native binary
// is needed: macOS kills a copied system binary (its platform signature
// doesn't survive the copy), and a script shows as its interpreter.
func buildSleeper(t *testing.T, dir string, names ...string) {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	src := t.TempDir()
	prog := "package main\n\nimport (\n\t\"os\"\n\t\"strconv\"\n\t\"time\"\n)\n\nfunc main() {\n\tn, _ := strconv.ParseFloat(os.Args[1], 64)\n\ttime.Sleep(time.Duration(n * float64(time.Second)))\n}\n"
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module sleeper\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		cmd := exec.Command(goBin, "build", "-o", filepath.Join(dir, name), ".")
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build: %v\n%s", err, out)
		}
	}
}

// interactiveShell is an interactive shell with no startup files to
// run in a pane — zsh -f where there is zsh, as in the QA report, else
// bash — and the name tmux reports for it at its prompt.
func interactiveShell(t *testing.T) (cmdline, name string) {
	t.Helper()
	if _, err := exec.LookPath("zsh"); err == nil {
		return "zsh -f", "zsh"
	}
	if _, err := exec.LookPath("bash"); err == nil {
		return "bash --norc --noprofile -i", "bash"
	}
	t.Skip("neither zsh nor bash installed")
	return "", ""
}

// waitCommand polls the session's first pane until its
// #{pane_current_command} satisfies ok, and returns it.
func waitCommand(ctx context.Context, t *testing.T, session string, ok func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		panes, err := ListPanes(ctx, session)
		if err != nil {
			t.Fatal(err)
		}
		if len(panes) > 0 && ok(panes[0].Command) {
			return panes[0].Command
		}
		if time.Now().After(deadline) {
			if len(panes) == 0 {
				t.Fatalf("%s: no panes", session)
			}
			t.Fatalf("%s: pane_current_command stayed %q", session, panes[0].Command)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestIntegration_PaneCurrentCommand pins what tmux reports as a pane's
// foreground command (Pane.Command), which the daemon uses to see an
// agent run by hand in a shell. The process name the OS has for the
// foreground job, not the command typed:
//
//   - a shell at its prompt: the shell;
//   - a native binary named `claude` it runs: `claude`, and the shell
//     again once it exits;
//   - a script named `claude`: its interpreter, never `claude`;
//   - a binary started through a symlink (Claude Code's native
//     installer: `claude` → versions/2.1.281): the binary's own name on
//     macOS, the name it was invoked by on Linux;
//   - a `sh -c "claude || fallback"` launch chain (how ccmux starts an
//     agent): the shell, whose children share its process group.
func TestIntegration_PaneCurrentCommand(t *testing.T) {
	ctx := isolatedServer(t)
	shellCmd, shell := interactiveShell(t)
	bin := t.TempDir()
	buildSleeper(t, bin, "claude", "2.1.281")
	scripts := t.TempDir()
	if err := os.WriteFile(filepath.Join(scripts, "claude"), []byte("#!/bin/sh\nsleep \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := t.TempDir()
	if err := os.Symlink(filepath.Join(bin, "2.1.281"), filepath.Join(link, "claude")); err != nil {
		t.Fatal(err)
	}
	if err := New(ctx, "fg", os.TempDir(), shellCmd); err != nil {
		t.Fatal(err)
	}
	is := func(want string) func(string) bool { return func(c string) bool { return c == want } }
	isNot := func(not string) func(string) bool { return func(c string) bool { return c != "" && c != not } }
	waitCommand(ctx, t, "fg", is(shell))
	run := func(cmdline string) {
		t.Helper()
		if err := SendText(ctx, "fg", cmdline); err != nil {
			t.Fatal(err)
		}
		if err := SendKeys(ctx, "fg", "Enter"); err != nil {
			t.Fatal(err)
		}
	}

	run(filepath.Join(bin, "claude") + " 2")
	waitCommand(ctx, t, "fg", is("claude"))
	waitCommand(ctx, t, "fg", is(shell)) // (b) after it exits

	run(filepath.Join(scripts, "claude") + " 2")
	got := waitCommand(ctx, t, "fg", isNot(shell))
	t.Logf("a #!/bin/sh script named claude shows as %q", got)
	if got == "claude" {
		t.Errorf("a script named claude showed as %q, want its interpreter", got)
	}
	waitCommand(ctx, t, "fg", is(shell))

	run(filepath.Join(link, "claude") + " 2")
	want := "claude"
	if runtime.GOOS == "darwin" {
		want = "2.1.281"
	}
	waitCommand(ctx, t, "fg", is(want))
	waitCommand(ctx, t, "fg", is(shell))

	if err := New(ctx, "chain", os.TempDir(), filepath.Join(bin, "claude")+" 30 || exec sleep 300"); err != nil {
		t.Fatal(err)
	}
	got = waitCommand(ctx, t, "chain", func(c string) bool { return c != "" })
	t.Logf("a `claude || fallback` launch chain shows as %q", got)
	if got == "claude" {
		t.Errorf("a launch chain showed as %q, want the shell running it", got)
	}
}
