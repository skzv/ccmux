package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestNewSessionForm_RejectsInvalidNames — tmux accepts `a:b` and
// `my.shell` as new-session names but then reads them as session:window
// targets, so the session could never be attached, previewed, killed or
// renamed ("can't find session: a"). The form must refuse them the way
// the rename form does, and still submit a valid name.
func TestNewSessionForm_RejectsInvalidNames(t *testing.T) {
	st := styles.Default()
	for _, bad := range []string{"a:b", "my.shell", "-dash", "has space"} {
		t.Run(bad, func(t *testing.T) {
			form := newNewSessionForm(st, nil, "", "")
			form.name.SetValue(bad)
			form, cmd := form.Update(keyMsg("enter"))
			if cmd != nil {
				if _, ok := cmd().(newBareSessionSubmitMsg); ok {
					t.Fatalf("form submitted invalid session name %q", bad)
				}
			}
			if form.err == "" {
				t.Errorf("no inline error for invalid name %q", bad)
			}
			if !strings.Contains(form.View(80), form.err) {
				t.Errorf("error %q not rendered in the form", form.err)
			}
		})
	}
	for _, good := range []string{"", "ok-name_1"} {
		form := newNewSessionForm(st, nil, "", "")
		form.name.SetValue(good)
		_, cmd := form.Update(keyMsg("enter"))
		if cmd == nil {
			t.Fatalf("valid name %q produced no submit", good)
		}
		if _, ok := cmd().(newBareSessionSubmitMsg); !ok {
			t.Errorf("valid name %q did not submit", good)
		}
	}
}

// TestSpawnBareSession_TagsAgentAtCreate — the local bare session must be
// created and tagged by one tmux call (NewWithAgent); tagging with a
// second call left a window in which ccmuxd classified a bare shell as
// Claude. An invalid name never reaches tmux.
func TestSpawnBareSession_TagsAgentAtCreate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	type call struct{ name, dir, cmdline, tag string }
	var calls []call
	orig := bareTmuxNew
	bareTmuxNew = func(_ context.Context, name, dir, cmdline, tag string) error {
		calls = append(calls, call{name, dir, cmdline, tag})
		return nil
	}
	t.Cleanup(func() { bareTmuxNew = orig })

	dir := t.TempDir()
	msg := spawnBareSessionCmd(newBareSessionSubmitMsg{Name: "plain", Path: dir, Host: "local"})()
	if _, ok := msg.(bareSessionReadyMsg); !ok {
		t.Fatalf("spawn returned %T (%v), want bareSessionReadyMsg", msg, msg)
	}
	msg = spawnBareSessionCmd(newBareSessionSubmitMsg{Name: "agentic", Path: dir, Host: "local", Agent: agent.IDCodex})()
	if _, ok := msg.(bareSessionReadyMsg); !ok {
		t.Fatalf("spawn returned %T (%v), want bareSessionReadyMsg", msg, msg)
	}
	if len(calls) != 2 {
		t.Fatalf("tmux create calls = %d, want 2", len(calls))
	}
	if calls[0].tag != "shell" || calls[1].tag != string(agent.IDCodex) {
		t.Errorf("agent tags = %q, %q; want shell, codex", calls[0].tag, calls[1].tag)
	}

	msg = spawnBareSessionCmd(newBareSessionSubmitMsg{Name: "a:b", Path: dir, Host: "local"})()
	if tm, ok := msg.(toastMsg); !ok || tm.Kind != toastError {
		t.Errorf("invalid name returned %T (%v), want an error toast", msg, msg)
	}
	if len(calls) != 2 {
		t.Errorf("invalid name reached tmux: %+v", calls[2:])
	}
}
