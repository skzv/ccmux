//go:build integration

package e2e

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/tmuxchrome"
)

// assertChromed fails the test if `name` is missing ccmux's status-bar
// chrome. tmuxchrome sets the session's status-left to a string
// containing the "ccmux" brand; a vanilla tmux session's status-left is
// the default "[#S] ", so the presence of "ccmux" is an unambiguous
// signal that chrome was applied.
func (e *Env) assertChromed(name string) {
	e.t.Helper()
	out, err := e.tmux("show-options", "-t", name, "status-left")
	if err != nil {
		e.t.Fatalf("show-options status-left %q: %v\n%s", name, err, out)
	}
	if !strings.Contains(out, "ccmux") {
		e.t.Errorf("session %q has no ccmux chrome — status-left = %q", name, strings.TrimSpace(out))
	}
}

func (e *Env) applyNestedChrome(name string) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tmuxchrome.Apply(ctx, name, "nested-test", false, true); err != nil {
		e.t.Fatalf("apply nested chrome to %q: %v", name, err)
	}
}

func (e *Env) unbindPrefixKey(key string) {
	e.t.Helper()
	_, _ = e.tmux("unbind-key", "-T", "prefix", key)
}

func (e *Env) prefixBindingLine(key string) string {
	e.t.Helper()
	out, err := e.tmux("list-keys", "-T", "prefix")
	if err != nil {
		e.t.Fatalf("list prefix keys: %v\n%s", err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		for i := 0; i+2 < len(fields); i++ {
			if fields[i] == "-T" && fields[i+1] == "prefix" && fields[i+2] == key {
				return strings.TrimSpace(line)
			}
		}
	}
	return ""
}

func (e *Env) statusRight(name string) string {
	e.t.Helper()
	out, err := e.tmux("show-options", "-t", name, "status-right")
	if err != nil {
		e.t.Fatalf("show-options status-right %q: %v\n%s", name, err, out)
	}
	return strings.TrimSpace(out)
}

// TestCLIChrome_AppliedOnCreate pins the fix for CLI-spawned sessions
// landing in vanilla green tmux. `ccmux attach`, `ccmux new`, and
// `ccmux resume` each create a tmux session and then attach to it; all
// three must apply ccmux's status-bar chrome first (the TUI and the
// daemon already did, only the CLI's three commands did not).
//
// The chrome layer is agent-agnostic, so attach is exercised against
// both a codex and an antigravity (gemini) project to lock in that the
// fix is not Claude-specific.
func TestCLIChrome_AppliedOnCreate(t *testing.T) {
	// attach onto an existing project, once per non-default agent — the
	// session's launch command comes from the .ccmux/agent sidecar.
	for _, tc := range []struct {
		name  string
		agent string
		dir   string
	}{
		{"attach_codex", "codex", "chrome-codex"},
		{"attach_gemini", "antigravity", "chrome-gemini"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			proj := filepath.Join(e.Root, tc.dir)
			writeFile(t, filepath.Join(proj, ".ccmux", "agent"), tc.agent)

			// `ccmux attach` creates the session then exec's `tmux
			// attach`, which fails without a tty — tolerated; chrome is
			// applied before the attach.
			_, _, _ = e.ccmux("attach", proj)

			session := "c-" + tc.dir
			if !e.hasSession(session) {
				t.Fatalf("`ccmux attach` did not create session %q", session)
			}
			e.assertChromed(session)
		})
	}

	t.Run("new", func(t *testing.T) {
		e := newEnv(t)
		_, _, _ = e.ccmuxIn(e.Root, "new", "chromenew")
		if !e.hasSession("c-chromenew") {
			t.Fatal("`ccmux new` did not create session c-chromenew")
		}
		e.assertChromed("c-chromenew")
	})

	t.Run("resume", func(t *testing.T) {
		e := newEnv(t)
		proj := filepath.Join(e.Root, "chrome-resume")
		mkdirAll(t, proj)
		const id = "chrome00-aaaa-bbbb-cccc-dddddddddddd"
		e.writeClaudeTranscript(id, proj, "chrome resume", "2026-05-19T15:00:00Z")

		_, _, _ = e.ccmux("resume", id)

		session := "c-resume-" + id[:8]
		if !e.hasSession(session) {
			t.Fatalf("`ccmux resume` did not create session %q", session)
		}
		e.assertChromed(session)
	})
}

func TestNestedChrome_ReturnBinding(t *testing.T) {
	t.Run("binds_backspace_and_hints_backspace_when_no_return_binding_exists", func(t *testing.T) {
		e := newEnv(t)
		const session = "c-nested-backspace"
		e.newTmuxSession(session, e.Home)
		e.unbindPrefixKey("L")
		e.unbindPrefixKey("BSpace")

		e.applyNestedChrome(session)

		if line := e.prefixBindingLine("BSpace"); !strings.Contains(line, "switch-client -l") {
			t.Fatalf("BSpace binding = %q, want switch-client -l", line)
		}
		status := e.statusRight(session)
		if !strings.Contains(status, "Ctrl-b then Backspace") {
			t.Fatalf("status-right = %q, want Backspace return hint", status)
		}
		if strings.Contains(status, "then L") {
			t.Fatalf("status-right = %q, should not keep stale L hint", status)
		}
	})

	t.Run("skips_occupied_backspace_and_hints_next_fallback", func(t *testing.T) {
		e := newEnv(t)
		const session = "c-nested-ctrl-g"
		e.newTmuxSession(session, e.Home)
		e.unbindPrefixKey("L")
		e.unbindPrefixKey("C-g")
		if out, err := e.tmux("bind-key", "BSpace", "display-message", "busy"); err != nil {
			t.Fatalf("bind occupied BSpace: %v\n%s", err, out)
		}

		e.applyNestedChrome(session)

		if line := e.prefixBindingLine("BSpace"); !strings.Contains(line, "display-message") || !strings.Contains(line, "busy") {
			t.Fatalf("BSpace binding = %q, want existing display-message busy binding preserved", line)
		}
		if line := e.prefixBindingLine("C-g"); !strings.Contains(line, "switch-client -l") {
			t.Fatalf("C-g binding = %q, want switch-client -l", line)
		}
		status := e.statusRight(session)
		if !strings.Contains(status, "Ctrl-b then Ctrl-g") {
			t.Fatalf("status-right = %q, want Ctrl-g return hint", status)
		}
		if strings.Contains(status, "then Backspace") {
			t.Fatalf("status-right = %q, should not advertise occupied Backspace", status)
		}
	})
}
