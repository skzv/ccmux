//go:build !windows

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
)

// TestNew_ExistingSessionSaysSo — `ccmux new alpha` while c-alpha was
// running failed with tmux's raw "duplicate session: c-alpha". It must
// say the project is already running and how to attach, without
// starting anything.
func TestNew_ExistingSessionSaysSo(t *testing.T) {
	e := newCLIEnv(t)
	e.mkdir("Projects/alpha")
	e.env["FAKE_TMUX_SESSIONS"] = "c-alpha"
	res := e.run("", "new", "alpha")
	if res.code == 0 {
		t.Fatalf("new over a running session should fail; stdout: %s", res.stdout)
	}
	for _, want := range []string{"already has a running session", "c-alpha", "ccmux attach alpha"} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("error should mention %q: %s", want, res.stderr)
		}
	}
	if got := e.tmuxCallsWith("new-session"); len(got) != 0 {
		t.Errorf("new must not try to start a second session: %v", got)
	}
}

// TestProject_AttachHintQuotesName — the "(none — `ccmux attach X`
// starts one)" hint printed a name with a space unquoted, so pasting it
// attached to a different project.
func TestProject_AttachHintQuotesName(t *testing.T) {
	e := newCLIEnv(t)
	root := e.mkdir("Projects")
	e.mkdir("Projects/with space/.git")
	res := e.run("", "--projects", root, "project", "with space")
	if res.code != 0 {
		t.Fatalf("project exit %d\n%s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "ccmux attach 'with space'") {
		t.Errorf("hint should quote the name:\n%s", res.stdout)
	}
}

// TestAgentsHelp_DescribesRealDiscovery — the agents / agents models
// help described an API-key-only, 24h catalog; discovery asks the
// claude CLI first, refreshes weekly, and --refresh runs at most once
// per 10 minutes (internal/claudemodels).
func TestAgentsHelp_DescribesRealDiscovery(t *testing.T) {
	e := newCLIEnv(t)
	for _, args := range [][]string{{"agents", "--help"}, {"agents", "models", "--help"}} {
		res := e.run("", args...)
		if res.code != 0 {
			t.Fatalf("%v exit %d", args, res.code)
		}
		help := strings.Join(strings.Fields(res.stdout), " ") // ignore line wrapping
		if strings.Contains(help, "24h") {
			t.Errorf("%v still says 24h:\n%s", args, help)
		}
		for _, want := range []string{"claude CLI", "weekly", "10 minutes"} {
			if !strings.Contains(help, want) {
				t.Errorf("%v help should mention %q:\n%s", args, want, help)
			}
		}
	}
}

// TestListHelp_NotClaudeOnly — `ccmux list` lists every agent's
// sessions, not just Claude's.
func TestListHelp_NotClaudeOnly(t *testing.T) {
	e := newCLIEnv(t)
	res := e.run("", "--help")
	if strings.Contains(res.stdout, "List Claude sessions") {
		t.Errorf("root help still describes list as Claude-only:\n%s", res.stdout)
	}
}

// TestListJSON_FallbackStateIsUnknown — with ccmuxd down, `list --json`
// emitted "state":"" for every tmux session; the protocol's value for
// "not classified" is "unknown".
func TestListJSON_FallbackStateIsUnknown(t *testing.T) {
	e := newCLIEnv(t)
	e.writeExe("tmux", `case "$1" in
list-sessions)
  case "$3" in
  *session_created*) printf 'c-alive\t1700000000\t1700000000\t0\t1\t/work/alive\n' ;;
  *) printf 'c-alive\t\n' ;;
  esac ;;
esac
exit 0
`)
	res := e.run("", "list", "--json")
	if res.code != 0 {
		t.Fatalf("list --json exit %d\n%s", res.code, res.stderr)
	}
	var got []daemon.SessionState
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil || len(got) != 1 {
		t.Fatalf("decode %q: %v", res.stdout, err)
	}
	if got[0].State != "unknown" {
		t.Errorf("fallback state = %q, want unknown", got[0].State)
	}
}

// TestUsage_HungDaemonSaysSo — against a daemon that accepts and never
// answers, `ccmux usage` told the user to "start ccmuxd first".
func TestUsage_HungDaemonSaysSo(t *testing.T) {
	t.Parallel() // waits out the 15s usage budget
	e := newCLIEnv(t)
	home, err := os.MkdirTemp("/tmp", "cxu")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	e.env["HOME"] = home
	sockDir := filepath.Join(home, ".local", "state", "ccmux")
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(sockDir, "ccmuxd.sock"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()

	res := e.run("", "usage")
	if res.code == 0 {
		t.Fatalf("usage against a hung daemon should fail; stdout: %s", res.stdout)
	}
	if strings.Contains(res.stderr, "start ccmuxd first") || !strings.Contains(res.stderr, "didn't respond") {
		t.Errorf("error should say the daemon didn't respond, not to start it: %s", res.stderr)
	}
}

// TestUsageError_Classifies — the three failure shapes get their own
// advice.
func TestUsageError_Classifies(t *testing.T) {
	dial := &net.OpError{Op: "dial", Net: "unix", Err: os.ErrNotExist}
	if msg := usageError(dial).Error(); !strings.Contains(msg, "ccmux daemon start") {
		t.Errorf("dial failure: %q, want a start hint", msg)
	}
	if msg := usageError(os.ErrPermission).Error(); strings.Contains(msg, "didn't respond") || strings.Contains(msg, "daemon start") {
		t.Errorf("other failure: %q, want a plain read error", msg)
	}
}

// TestDaemonDown_CommandsSayHowToStartIt — with ccmuxd not running,
// `ccmux notes list` (and read/search, pair, shell) printed the raw
// "dial unix …: connect: connection refused" with no hint, while
// `ccmux usage` said to start the daemon. Every command that needs the
// local daemon must give the same hint, whether the socket is missing
// or stale (a crashed daemon's, nothing listening: "connection refused").
func TestDaemonDown_CommandsSayHowToStartIt(t *testing.T) {
	commands := [][]string{
		{"notes", "list", "proj"},
		{"notes", "read", "proj", "README.md"},
		{"notes", "search", "proj", "needle"},
		{"pair"},
		{"shell", "--agent", "shell"},
		{"usage"},
	}
	for socket, dialErr := range map[string]string{
		"missing": "no such file or directory",
		"stale":   "connection refused",
	} {
		for _, args := range commands {
			t.Run(socket+"/"+strings.Join(args[:min(2, len(args))], "-"), func(t *testing.T) {
				e := newCLIEnv(t)
				if socket == "stale" {
					e.staleDaemonSocket()
				} else {
					e.daemonSocketPath() // a $HOME short enough to dial; nothing there
				}
				res := e.run("", args...)
				if res.code == 0 {
					t.Fatalf("ccmux %v with no daemon exited 0; stdout %q", args, res.stdout)
				}
				if !strings.Contains(res.stderr, "ccmux daemon start") {
					t.Errorf("ccmux %v with no daemon: stderr should say how to start it: %s", args, res.stderr)
				}
				if !strings.Contains(res.stderr, dialErr) {
					t.Errorf("setup: a %s socket should fail the dial with %q: %s", socket, dialErr, res.stderr)
				}
			})
		}
	}
}

// TestDaemonDownErr_OnlyForAFailedDial — the start hint is for a
// daemon that can't be reached at all; a daemon that answered with an
// error is reported by the caller, unchanged.
func TestDaemonDownErr_OnlyForAFailedDial(t *testing.T) {
	dial := &net.OpError{Op: "dial", Net: "unix", Err: os.ErrNotExist}
	if err := daemonDownErr(fmt.Errorf("ccmuxd x GET /v1/notes: %w", dial)); err == nil || !strings.Contains(err.Error(), "ccmux daemon start") {
		t.Errorf("wrapped dial failure: %v, want the start hint", err)
	}
	if err := daemonDownErr(errors.New("ccmuxd x GET /v1/notes: status 404: project not found")); err != nil {
		t.Errorf("an answer from the daemon: %v, want nil", err)
	}
	if err := notesErr(false, dial); err != dial {
		t.Errorf("a peer's dial failure: %v, want it unchanged (the start hint is for this device)", err)
	}
}
