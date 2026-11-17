//go:build integration

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// clipboardText is what the test session prints; mouse selections are
// cut from its first line.
const clipboardText = "hello-ccmux-world"

// TestClipboard_CopyLandsOnTheDeviceThatMadeIt — a Mac (local client)
// and a phone (a client that came in over SSH) attached to the same
// session. A selection made on the phone must stay off this machine's
// clipboard; one made on the Mac must land on it. Before the binding
// named the copying client, the phone's copy went to pbcopy too,
// because "a local client is attached" was all `ccmux clipboard-pipe`
// could see.
//
// It runs the shipped pieces end to end: the server starts with an
// older ccmux's binding, ccmuxd upgrades it in place, `ccmux doctor`
// reports both states, and real mouse drags on real tmux clients fire
// the binding into the built `ccmux clipboard-pipe`. A fake pbcopy /
// wl-copy stands in for the native clipboard so the test never touches
// the real one.
func TestClipboard_CopyLandsOnTheDeviceThatMadeIt(t *testing.T) {
	e := newEnv(t)
	tools := filepath.Join(e.Home, "clipbin")
	clips := filepath.Join(e.Home, "clips")
	runs := filepath.Join(e.Home, "runs")
	for _, d := range []string{tools, clips, runs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(tools, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The native clipboard tools clipboard-pipe looks up on PATH:
	// pbcopy on macOS, wl-copy first on Linux.
	for _, tool := range []string{"pbcopy", "wl-copy"} {
		write(tool, fmt.Sprintf("cat > %q/clip.$$\n", clips))
	}
	// Wrap the built ccmux so the test can tell when each copy-pipe run
	// has finished (and how it exited), including the runs that leave
	// the clipboard alone.
	write("ccmux", fmt.Sprintf("%q \"$@\"\necho $? > %q/run.$$\n", builtCcmux, runs))
	// The tmux server inherits this PATH, and copy-pipe jobs run with it.
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))

	if out, err := e.tmux("new-session", "-d", "-s", "clip", "-x", "80", "-y", "24",
		"printf '"+clipboardText+"\\n'; exec cat"); err != nil {
		t.Fatalf("new-session: %v\n%s", err, out)
	}
	if out, err := e.tmux("set", "-g", "mouse", "on"); err != nil {
		t.Fatalf("mouse on: %v\n%s", err, out)
	}
	// An install from before the binding named the copying client.
	for _, table := range []string{"copy-mode-vi", "copy-mode"} {
		if out, err := e.tmux("bind-key", "-T", table, "MouseDragEnd1Pane",
			"send-keys", "-X", "copy-pipe-no-clear", "ccmux clipboard-pipe"); err != nil {
			t.Fatalf("bind legacy: %v\n%s", err, out)
		}
	}
	if out, _, _ := e.ccmux("doctor"); !strings.Contains(out, "upgrade pending") {
		t.Errorf("doctor should report the older binding as an upgrade pending; got:\n%s", out)
	}

	e.startDaemon()
	upgraded := func() bool {
		out, _ := e.tmux("list-keys", "-T", "copy-mode")
		return strings.Contains(out, "--client")
	}
	if !waitFor(10*time.Second, upgraded) {
		t.Fatal("ccmuxd did not replace the older clipboard binding")
	}
	if out, _, _ := e.ccmux("doctor"); !strings.Contains(out, "✓ tmux mouse-copy binding") {
		t.Errorf("doctor should report the binding as current; got:\n%s", out)
	}

	mac := attachClipClient(t, e, false)
	phone := attachClipClient(t, e, true)

	seen := map[string]bool{}
	nextRun := func(who string) {
		t.Helper()
		var code string
		if !waitFor(10*time.Second, func() bool {
			ents, _ := os.ReadDir(runs)
			for _, ent := range ents {
				if !seen[ent.Name()] {
					seen[ent.Name()] = true
					b, _ := os.ReadFile(filepath.Join(runs, ent.Name()))
					code = strings.TrimSpace(string(b))
					return true
				}
			}
			return false
		}) {
			t.Fatalf("drag on %s: the copy binding never ran ccmux clipboard-pipe", who)
		}
		if code != "0" {
			t.Errorf("drag on %s: ccmux clipboard-pipe exited %s", who, code)
		}
	}
	clipped := func() []string {
		ents, _ := os.ReadDir(clips)
		var got []string
		for _, ent := range ents {
			b, _ := os.ReadFile(filepath.Join(clips, ent.Name()))
			got = append(got, string(b))
		}
		return got
	}

	dragSelect(t, phone)
	nextRun("phone")
	if got := clipped(); len(got) != 0 {
		t.Fatalf("a copy made on the phone reached this machine's clipboard: %q", got)
	}
	if out, err := e.tmux("send-keys", "-t", "clip", "-X", "cancel"); err != nil {
		t.Fatalf("leave copy mode: %v\n%s", err, out)
	}
	time.Sleep(200 * time.Millisecond)

	dragSelect(t, mac)
	nextRun("mac")
	got := clipped()
	if len(got) != 1 || got[0] == "" || !strings.Contains(clipboardText, got[0]) {
		t.Fatalf("a copy made on the Mac should reach this machine's clipboard once, as part of %q; got %q", clipboardText, got)
	}
}

// attachClipClient attaches a real tmux client to the "clip" session on
// its own pty: one from this machine, or one that came in over SSH (a
// phone over Moshi carries SSH_CONNECTION the same way).
func attachClipClient(t *testing.T, e *Env, overSSH bool) *os.File {
	t.Helper()
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "SSH_CONNECTION=") && !strings.HasPrefix(kv, "CCMUX_REMOTE_CLIENT=") {
			env = append(env, kv)
		}
	}
	env = append(env, "TERM=xterm-256color")
	if overSSH {
		env = append(env, "SSH_CONNECTION=100.64.0.2 50000 100.64.0.1 22")
	}
	cmd := exec.Command("tmux", "attach", "-t", "clip")
	cmd.Env = env
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	go func() { // drain the client's screen so tmux never blocks on it
		buf := make([]byte, 32*1024)
		for {
			if _, err := ptmx.Read(buf); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = ptmx.Close()
	})
	pid := fmt.Sprint(cmd.Process.Pid)
	if !waitFor(5*time.Second, func() bool {
		out, _ := e.tmux("list-clients", "-F", "#{client_pid}")
		return strings.Contains("\n"+out, "\n"+pid+"\n")
	}) {
		t.Fatalf("client %s never attached", pid)
	}
	time.Sleep(300 * time.Millisecond) // first redraw
	return ptmx
}

// dragSelect drag-selects part of the pane's first line the way a user
// does — SGR mouse press, motion, release — so tmux enters copy mode
// and fires the MouseDragEnd1Pane binding on the release.
func dragSelect(t *testing.T, ptmx *os.File) {
	t.Helper()
	for _, seq := range []string{"\x1b[<0;2;1M", "\x1b[<32;5;1M", "\x1b[<32;9;1M", "\x1b[<0;9;1m"} {
		if _, err := ptmx.Write([]byte(seq)); err != nil {
			t.Fatalf("mouse event: %v", err)
		}
		time.Sleep(150 * time.Millisecond) // separate events, not one read
	}
}
