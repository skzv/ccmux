//go:build integration

package clipboard

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// These tests drive a real tmux server — an isolated one, via
// TMUX_TMPDIR — with real clients attached through ptys, to pin what
// the clipboard bindings rely on: that tmux expands #{client_tty} in a
// copy-pipe command to the tty of the client that made the selection.
// tmux has done so since 3.0 (window-copy.c passes the client to
// format_single; 2.x passed NULL, so client formats came out empty);
// CI runs this on Ubuntu's tmux 3.4 and Homebrew's current release.

// paneText is what the test session prints; selections are cut from it.
const paneText = "hello-ccmux-world"

// copyRig is an isolated tmux server with ccmux's clipboard setup
// applied and a recorder standing in for `ccmux` on the server's PATH:
// each copy-pipe run leaves the argv it got and the selection it read.
type copyRig struct {
	t      *testing.T
	ctx    context.Context
	recDir string
}

func newCopyRig(t *testing.T) *copyRig {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir, err := os.MkdirTemp("/tmp", "ccclip") // short: sockaddr_un limit
	if err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(dir, "bin")
	recDir := filepath.Join(dir, "rec")
	for _, d := range []string{binDir, recDir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The selection is written first and the argv file renamed into
	// place last, so an argv file's presence means the run finished.
	recorder := "#!/bin/sh\nd=" + recDir + "\nn=$$\ncat > \"$d/stdin.$n\"\n" +
		"printf '%s\\n' \"$@\" > \"$d/tmp.$n\" && mv \"$d/tmp.$n\" \"$d/argv.$n\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "ccmux"), []byte(recorder), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Setenv("HOME", dir)
	// The server inherits this PATH, and copy-pipe jobs run with it.
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	r := &copyRig{t: t, ctx: ctx, recDir: recDir}
	r.tmux("-f", "/dev/null", "new-session", "-d", "-s", "clip", "-x", "80", "-y", "24",
		"printf '"+paneText+"\\n'; exec cat")
	r.tmux("set", "-g", "mouse", "on")
	return r
}

func (r *copyRig) tmux(args ...string) string {
	r.t.Helper()
	out, err := exec.CommandContext(r.ctx, "tmux", args...).CombinedOutput()
	if err != nil {
		r.t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// rigClient is one real `tmux attach` running on its own pty.
type rigClient struct {
	pid  int
	ptmx *os.File
	mu   sync.Mutex
	out  bytes.Buffer
}

func (r *copyRig) attach() *rigClient {
	r.t.Helper()
	cmd := exec.Command("tmux", "attach", "-t", "clip")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		r.t.Fatalf("attach client: %v", err)
	}
	c := &rigClient{pid: cmd.Process.Pid, ptmx: ptmx}
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			c.mu.Lock()
			c.out.Write(buf[:n])
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	r.t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = ptmx.Close()
	})
	// Wait until the server lists it, so a drag isn't sent to a client
	// tmux hasn't finished setting up.
	if !waitUntil(5*time.Second, func() bool {
		cs, _ := listTmuxClients(r.ctx)
		for _, x := range cs {
			if x.PID == c.pid && x.TTY != "" {
				return true
			}
		}
		return false
	}) {
		r.t.Fatalf("client pid %d never showed up in list-clients", c.pid)
	}
	time.Sleep(300 * time.Millisecond) // first redraw
	return c
}

func (c *rigClient) sawOSC52() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return bytes.Contains(c.out.Bytes(), []byte("\x1b]52;"))
}

func (c *rigClient) resetOutput() {
	c.mu.Lock()
	c.out.Reset()
	c.mu.Unlock()
}

// drag selects part of the pane's first line with the mouse, the way
// a user does: SGR press, two motion reports, release. tmux enters
// copy mode on the drag and fires MouseDragEnd1Pane on the release —
// the binding under test.
func (c *rigClient) drag(t *testing.T) {
	t.Helper()
	for _, seq := range []string{"\x1b[<0;2;1M", "\x1b[<32;5;1M", "\x1b[<32;9;1M", "\x1b[<0;9;1m"} {
		if _, err := c.ptmx.Write([]byte(seq)); err != nil {
			t.Fatalf("write mouse event: %v", err)
		}
		time.Sleep(150 * time.Millisecond) // separate events, not one read
	}
}

// copyRun is one recorded invocation of the copy-pipe command.
type copyRun struct {
	argv      []string
	selection string
}

// nextRun waits for a recorder run not in seen and returns it.
func (r *copyRig) nextRun(seen map[string]bool) copyRun {
	r.t.Helper()
	var name string
	if !waitUntil(5*time.Second, func() bool {
		ents, _ := os.ReadDir(r.recDir)
		var fresh []string
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), "argv.") && !seen[e.Name()] {
				fresh = append(fresh, e.Name())
			}
		}
		sort.Strings(fresh)
		if len(fresh) > 0 {
			name = fresh[0]
		}
		return name != ""
	}) {
		r.t.Fatal("the copy-mode binding never ran its pipe command")
	}
	seen[name] = true
	argv, _ := os.ReadFile(filepath.Join(r.recDir, name))
	sel, _ := os.ReadFile(filepath.Join(r.recDir, "stdin."+strings.TrimPrefix(name, "argv.")))
	return copyRun{argv: strings.Split(strings.TrimSuffix(string(argv), "\n"), "\n"), selection: string(sel)}
}

func waitUntil(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}

// TestIntegration_CopyPipeNamesTheCopyingClient — two clients on one
// session, each drag-selecting in turn, in both copy-mode tables: the
// pipe must be told the tty of the client that made each selection,
// and that tty must resolve, through the pipe's own list-clients
// parsing, to that client's PID.
func TestIntegration_CopyPipeNamesTheCopyingClient(t *testing.T) {
	r := newCopyRig(t)
	if err := EnableTmuxClipboard(r.ctx); err != nil {
		t.Fatalf("EnableTmuxClipboard: %v", err)
	}
	a := r.attach()
	b := r.attach()
	clients, err := listTmuxClients(r.ctx)
	if err != nil {
		t.Fatalf("list clients: %v", err)
	}
	ttyOf := map[int]string{}
	for _, c := range clients {
		ttyOf[c.PID] = c.TTY
	}
	if ttyOf[a.pid] == "" || ttyOf[b.pid] == "" || ttyOf[a.pid] == ttyOf[b.pid] {
		t.Fatalf("want two clients with distinct ttys, got %+v (a=%d b=%d)", clients, a.pid, b.pid)
	}
	t.Logf("%s; client a=%s b=%s", strings.TrimSpace(r.tmux("-V")), ttyOf[a.pid], ttyOf[b.pid])

	seen := map[string]bool{}
	for _, mode := range []string{"emacs", "vi"} { // copy-mode, copy-mode-vi
		r.tmux("set", "-g", "mode-keys", mode)
		for _, who := range []struct {
			name string
			c    *rigClient
		}{{"a", a}, {"b", b}} {
			a.resetOutput()
			b.resetOutput()
			who.c.drag(t)
			run := r.nextRun(seen)
			want := []string{"clipboard-pipe", "--client", ttyOf[who.c.pid]}
			if strings.Join(run.argv, "\x00") != strings.Join(want, "\x00") {
				t.Errorf("mode-keys %s, drag on %s: pipe argv = %q, want %q", mode, who.name, run.argv, want)
			}
			if run.selection == "" || !strings.Contains(paneText, run.selection) {
				t.Errorf("mode-keys %s, drag on %s: selection %q is not part of %q", mode, who.name, run.selection, paneText)
			}
			if got, ok := copyingClient(run.argv[len(run.argv)-1], clients); !ok || got.PID != who.c.pid {
				t.Errorf("mode-keys %s, drag on %s: --client resolves to %+v (ok=%v), want pid %d", mode, who.name, got, ok, who.c.pid)
			}
			// tmux's own OSC 52 (set-clipboard on) goes to every client
			// showing the pane, not just the copier — logged, not
			// asserted: it's tmux's behaviour, not ccmux's.
			t.Logf("mode-keys %s, drag on %s: OSC 52 reached a=%v b=%v", mode, who.name, waitUntil(time.Second, a.sawOSC52), waitUntil(time.Second, b.sawOSC52))
			r.tmux("send-keys", "-t", "clip", "-X", "cancel")
			time.Sleep(200 * time.Millisecond)
		}
	}
}

// copyBindings returns tmux's listing of the MouseDragEnd1Pane binding
// in each copy-mode table.
func (r *copyRig) copyBindings() []string {
	r.t.Helper()
	var lines []string
	for _, table := range []string{"copy-mode", "copy-mode-vi"} {
		for _, ln := range strings.Split(r.tmux("list-keys", "-T", table), "\n") {
			if strings.Contains(ln, "MouseDragEnd1Pane") {
				lines = append(lines, strings.Join(strings.Fields(ln), " "))
			}
		}
	}
	return lines
}

// TestIntegration_ClipboardBindingsUpgradeInPlace — ccmuxd re-applies
// the bindings on every tmux server it meets, so a server still
// carrying an older ccmux's binding (no --client) must end up with
// exactly one binding per table, the new one; and the tmux.conf
// snippet must produce the very same bindings when sourced.
func TestIntegration_ClipboardBindingsUpgradeInPlace(t *testing.T) {
	r := newCopyRig(t)
	if bs, err := TmuxCopyBindingState(r.ctx); err != nil || bs != BindingMissing {
		t.Errorf("fresh server: binding state %v (err %v), want missing", bs, err)
	}
	for _, table := range []string{"copy-mode-vi", "copy-mode"} {
		r.tmux("bind-key", "-T", table, "MouseDragEnd1Pane", "send-keys", "-X", "copy-pipe-no-clear", "ccmux clipboard-pipe")
	}
	if bs, err := TmuxCopyBindingState(r.ctx); err != nil || bs != BindingLegacy {
		t.Errorf("older ccmux's binding: state %v (err %v), want legacy", bs, err)
	}
	for i := 0; i < 2; i++ { // the daemon may apply them more than once
		if err := EnableTmuxClipboard(r.ctx); err != nil {
			t.Fatalf("EnableTmuxClipboard: %v", err)
		}
	}
	if bs, err := TmuxCopyBindingState(r.ctx); err != nil || bs != BindingCurrent {
		t.Errorf("after EnableTmuxClipboard: state %v (err %v), want current", bs, err)
	}
	applied := r.copyBindings()
	if len(applied) != 2 {
		t.Fatalf("want one MouseDragEnd1Pane binding per copy table, got %d:\n%s", len(applied), strings.Join(applied, "\n"))
	}
	for _, ln := range applied {
		if !strings.Contains(ln, "--client '#{client_tty}'") {
			t.Errorf("binding not upgraded: %s", ln)
		}
	}

	conf := filepath.Join(t.TempDir(), "ccmux.tmux.conf")
	if err := os.WriteFile(conf, []byte(SuggestTmuxConf()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"copy-mode-vi", "copy-mode"} {
		r.tmux("bind-key", "-T", table, "MouseDragEnd1Pane", "send-keys", "-X", "copy-pipe-no-clear", "ccmux clipboard-pipe")
	}
	r.tmux("source-file", conf)
	if sourced := r.copyBindings(); strings.Join(sourced, "\n") != strings.Join(applied, "\n") {
		t.Errorf("SuggestTmuxConf binds\n%s\nbut ccmuxd binds\n%s", strings.Join(sourced, "\n"), strings.Join(applied, "\n"))
	}
}
