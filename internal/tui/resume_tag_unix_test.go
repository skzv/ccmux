//go:build !windows

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
)

// TestResumeConversation_TagsInTheCreatingTmuxCall — the TUI's resume
// ran `new-session` and then a separate `set-option @ccmux_agent`, so a
// daemon poll tick in between classified the resumed session with its
// project's agent (a Codex conversation in a Claude project read as a
// crashed Claude). The tag must ride on the new-session invocation
// (tmux's `;` command separator), with no set-option call of its own.
// Driven through a fake tmux on PATH, so it checks the real tmux calls.
func TestResumeConversation_TagsInTheCreatingTmuxCall(t *testing.T) {
	bin := t.TempDir()
	logPath := filepath.Join(bin, "tmux.log")
	fake := "#!/bin/sh\nprintf '%s|' \"$@\" >> \"$FAKE_TMUX_LOG\"\nprintf '\\n' >> \"$FAKE_TMUX_LOG\"\n[ \"$1\" = has-session ] && exit 1\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("FAKE_TMUX_LOG", logPath)
	t.Setenv("TMUX", "")

	a := newAppForTest(t)
	c := conversations.Conversation{ID: "0198a3c2-1b2c-7d3e-8f9a-0b1c2d3e4f5a", Agent: agent.IDCodex, Project: t.TempDir()}
	msg, ok := a.resumeConversationCmd(c)().(conversationResumedMsg)
	if !ok || msg.Err != nil {
		t.Fatalf("resume: %+v", msg)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	name := conversations.ResumeSessionName(c.ID)
	var created bool
	for _, call := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		switch {
		case strings.HasPrefix(call, "new-session|"):
			created = strings.Contains(call, "|-s|"+name+"|") &&
				strings.Contains(call, "|;|set-option|") && strings.Contains(call, "|@ccmux_agent|codex|")
		case strings.HasPrefix(call, "set-option|"):
			t.Errorf("a separate set-option call leaves the session untagged in between: %s", call)
		}
	}
	if !created {
		t.Errorf("new-session for %s must carry the agent tag in the same call; tmux calls:\n%s", name, raw)
	}
}
