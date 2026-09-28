package cmd

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/tmux"
)

// TestShellQuote — local copy of the helper, must behave the same
// way as the TUI's. The single-quote escape ('foo'\”bar') is the
// canonical POSIX trick; getting it wrong breaks remote attach for
// any session with a quote in its name.
func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"c-foo", "'c-foo'"},
		{"c-foo bar", "'c-foo bar'"},
		{"c'with'quotes", `'c'\''with'\''quotes'`},
		{"", "''"},
	}
	for _, tc := range cases {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShellAttachCommandsOmitDetachFlag(t *testing.T) {
	t.Setenv("TMUX", "") // standalone attach; the nested case has its own test
	local := shellAttachCmd("c-foo")
	if got := strings.Join(local.Args, " "); got != "tmux attach-session -t =c-foo:" {
		t.Errorf("local shell attach args = %q, want mirror attach without -d", got)
	}

	remote := remoteShellTmuxAttach("c-foo")
	if strings.Contains(remote, " -d ") {
		t.Errorf("remote shell attach should not pass -d: %q", remote)
	}
	if !strings.Contains(remote, " tmux attach-session -t '=c-foo:'") {
		t.Errorf("remote shell attach should use mirror attach on the exact target: %q", remote)
	}
}

// TestRemoteShellTmuxAttach_ExactTarget — the remote attach sent a bare
// `-t '%1'`, which tmux reads as pane %1: a session named "%1" (the
// daemon allows it) attached to whichever session held that pane. It
// must use the exact `=name:` form, as the TUI's remote attach does.
func TestRemoteShellTmuxAttach_ExactTarget(t *testing.T) {
	for _, name := range []string{"%1", "@1", "c-foo"} {
		got := remoteShellTmuxAttach(name)
		if want := " tmux attach-session -t " + shellQuote(tmux.ExactSession(name)); !strings.HasSuffix(got, want) {
			t.Errorf("remoteShellTmuxAttach(%q) = %q, want it to end in %q", name, got, want)
		}
	}
}

// TestRemoteAttachHint_SurvivesBothShells — the printed hint is parsed
// twice: by the user's shell, then (ssh joins its arguments) by the
// remote login shell. Both passes must leave tmux the exact target.
func TestRemoteAttachHint_SurvivesBothShells(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	words := func(cmdline string) []string {
		t.Helper()
		out, err := exec.Command(sh, "-c", `for w in `+cmdline+`; do printf '%s\n' "$w"; done`).Output()
		if err != nil {
			t.Fatalf("sh parse %q: %v", cmdline, err)
		}
		return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	}
	for _, name := range []string{"c-shell-1", "%1", "@work", "a b", "it's", "a$HOME", "x`id`"} {
		hint := remoteAttachHint("me@mini", name)
		local := words(hint)
		if len(local) != 4 || local[0] != "ssh" || local[1] != "-t" || local[2] != "me@mini" {
			t.Errorf("hint %q parses locally as %q; want ssh -t me@mini <one remote command>", hint, local)
			continue
		}
		remote := words(local[3])
		want := []string{"tmux", "attach-session", "-t", tmux.ExactSession(name)}
		if strings.Join(remote, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("hint %q runs %q remotely, want %q", hint, remote, want)
		}
	}
	if got := remoteAttachHint("me@mini", "c-shell-1"); got != `ssh -t me@mini "tmux attach-session -t '=c-shell-1:'"` {
		t.Errorf("common-case hint = %s; want it readable", got)
	}
}

// TestNewCmdAgent — `ccmux new` honours agents.default like the TUI
// form does; --agent still wins, and a bad flag lists every agent.
func TestNewCmdAgent(t *testing.T) {
	if id, err := newCmdAgent("", "codex"); err != nil || id != agent.IDCodex {
		t.Errorf("config default codex: got %q, %v", id, err)
	}
	if id, err := newCmdAgent("cursor", "codex"); err != nil || id != agent.IDCursor {
		t.Errorf("--agent should win: got %q, %v", id, err)
	}
	if id, err := newCmdAgent("", "shell"); err != nil || id != "" {
		t.Errorf("non-agent default should fall back: got %q, %v", id, err)
	}
	_, err := newCmdAgent("nope", "")
	if err == nil {
		t.Fatal("unknown --agent accepted")
	}
	for _, a := range agent.All() {
		if !strings.Contains(err.Error(), string(a.ID())) {
			t.Errorf("error %q doesn't list agent %q", err, a.ID())
		}
	}
}
