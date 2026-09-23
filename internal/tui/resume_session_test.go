package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
)

// fakeResumeTmux swaps resumeConversationCmd's tmux seams for an
// in-memory session set, so no test ever touches a real tmux server.
type fakeResumeTmux struct {
	sessions map[string]bool
	tagged   []string
	killed   []string
}

func installFakeResumeTmux(t *testing.T) *fakeResumeTmux {
	t.Helper()
	f := &fakeResumeTmux{sessions: map[string]bool{}}
	origNew, origHas, origTag, origKill := resumeTmuxNew, resumeTmuxHas, resumeTmuxSetAgent, resumeTmuxKill
	t.Cleanup(func() {
		resumeTmuxNew, resumeTmuxHas, resumeTmuxSetAgent, resumeTmuxKill = origNew, origHas, origTag, origKill
	})
	resumeTmuxNew = func(_ context.Context, name, _, _ string) error {
		if f.sessions[name] {
			return errors.New("duplicate session: " + name)
		}
		f.sessions[name] = true
		return nil
	}
	resumeTmuxHas = func(_ context.Context, name string) (bool, error) { return f.sessions[name], nil }
	resumeTmuxSetAgent = func(_ context.Context, name, _ string) error {
		f.tagged = append(f.tagged, name)
		return nil
	}
	resumeTmuxKill = func(_ context.Context, name string) error {
		f.killed = append(f.killed, name)
		delete(f.sessions, name)
		return nil
	}
	return f
}

// TestResumeConversation_SecondResumeAttachesToExisting — regression:
// resuming the same conversation twice from the TUI failed with tmux's
// "duplicate session" (the session name is deterministic), while
// `ccmux resume` attached to the running session. The TUI must attach
// too — without re-tagging or killing the existing session.
func TestResumeConversation_SecondResumeAttachesToExisting(t *testing.T) {
	f := installFakeResumeTmux(t)
	a := newAppForTest(t)
	c := conversations.Conversation{ID: "5f3c1d2e-aaaa-4bbb-8ccc-0123456789ab", Agent: agent.IDClaude, Project: t.TempDir()}

	first, ok := a.resumeConversationCmd(c)().(conversationResumedMsg)
	if !ok || first.Err != nil {
		t.Fatalf("first resume: %+v", first)
	}
	if first.Existing {
		t.Error("first resume created the session; Existing should be false")
	}

	second, ok := a.resumeConversationCmd(c)().(conversationResumedMsg)
	if !ok {
		t.Fatal("second resume produced no conversationResumedMsg")
	}
	if second.Err != nil {
		t.Fatalf("second resume failed: %v", second.Err)
	}
	if second.Session != first.Session || !second.Existing {
		t.Errorf("second resume = %+v, want Existing attach to %q", second, first.Session)
	}
	if len(f.tagged) != 1 || len(f.killed) != 0 {
		t.Errorf("existing session was re-tagged or killed (tagged=%v killed=%v)", f.tagged, f.killed)
	}
}

// TestResumeConversation_UUIDv7NeighboursGetOwnSessions — two Codex
// conversations whose UUIDv7 IDs share the first 8 hex digits (started
// within ~65s of each other) must resume into different sessions; the
// old first-8-chars name attached the second to the first's session.
func TestResumeConversation_UUIDv7NeighboursGetOwnSessions(t *testing.T) {
	f := installFakeResumeTmux(t)
	a := newAppForTest(t)
	c1 := conversations.Conversation{ID: "0198a3c2-1b2c-7d3e-8f9a-0b1c2d3e4f5a", Agent: agent.IDCodex, Project: t.TempDir()}
	c2 := conversations.Conversation{ID: "0198a3c2-9e8d-7c6b-a5f4-e3d2c1b0a998", Agent: agent.IDCodex, Project: t.TempDir()}

	m1 := a.resumeConversationCmd(c1)().(conversationResumedMsg)
	m2 := a.resumeConversationCmd(c2)().(conversationResumedMsg)
	if m1.Err != nil || m2.Err != nil {
		t.Fatalf("resume errors: %v / %v", m1.Err, m2.Err)
	}
	if m1.Session == m2.Session || m2.Existing {
		t.Fatalf("distinct conversations share session %q (second Existing=%v)", m1.Session, m2.Existing)
	}
	if m1.Session != conversations.ResumeSessionName(c1.ID) || m2.Session != conversations.ResumeSessionName(c2.ID) {
		t.Errorf("TUI session names %q/%q must come from conversations.ResumeSessionName (shared with the CLI)", m1.Session, m2.Session)
	}
	if len(f.sessions) != 2 {
		t.Errorf("sessions created = %d, want 2", len(f.sessions))
	}
}
