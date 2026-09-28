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
// sessions maps each session to its agent tag; tagged lists sessions
// tagged by a separate set-option call.
type fakeResumeTmux struct {
	sessions map[string]string
	tagged   []string
	killed   []string
}

func installFakeResumeTmux(t *testing.T) *fakeResumeTmux {
	t.Helper()
	f := &fakeResumeTmux{sessions: map[string]string{}}
	origNew, origHas, origTag, origKill := resumeTmuxNew, resumeTmuxHas, resumeTmuxSetAgent, resumeTmuxKill
	t.Cleanup(func() {
		resumeTmuxNew, resumeTmuxHas, resumeTmuxSetAgent, resumeTmuxKill = origNew, origHas, origTag, origKill
	})
	resumeTmuxNew = func(_ context.Context, name, _, _, tag string) error {
		if _, ok := f.sessions[name]; ok {
			return errors.New("tmux new-session: exit status 1 (duplicate session: " + name + ")")
		}
		f.sessions[name] = tag
		return nil
	}
	resumeTmuxHas = func(_ context.Context, name string) (bool, error) {
		_, ok := f.sessions[name]
		return ok, nil
	}
	resumeTmuxSetAgent = func(_ context.Context, name, tag string) error {
		f.tagged = append(f.tagged, name)
		f.sessions[name] = tag
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
	if len(f.tagged) != 0 || len(f.killed) != 0 {
		t.Errorf("session tagged by a separate call, or the existing one killed (tagged=%v killed=%v)", f.tagged, f.killed)
	}
	if got := f.sessions[first.Session]; got != string(agent.IDClaude) {
		t.Errorf("session created with agent tag %q, want claude", got)
	}
}

// TestResumeConversation_TagFailureHandling — when new-session ran but
// the set-option half of the same tmux call failed, the session is
// tagged again, and killed if that fails too so no untagged session is
// left for the next resume to attach to. A session a concurrent resume
// created first ("duplicate session") is attached to, never re-tagged
// or killed.
func TestResumeConversation_TagFailureHandling(t *testing.T) {
	c := conversations.Conversation{ID: "5f3c1d2e-aaaa-4bbb-8ccc-0123456789ab", Agent: agent.IDCodex, Project: t.TempDir()}
	name := conversations.ResumeSessionName(c.ID)

	f := installFakeResumeTmux(t)
	a := newAppForTest(t)
	resumeTmuxNew = func(_ context.Context, name, _, _, _ string) error {
		f.sessions[name] = ""
		return errors.New("tmux new-session: exit status 1 (invalid option: @ccmux_agent)")
	}
	msg := a.resumeConversationCmd(c)().(conversationResumedMsg)
	if msg.Err != nil || msg.Existing || f.sessions[name] != string(agent.IDCodex) {
		t.Errorf("set-option half failed: msg=%+v tag=%q, want the new session re-tagged codex", msg, f.sessions[name])
	}

	delete(f.sessions, name)
	resumeTmuxSetAgent = func(context.Context, string, string) error { return errors.New("set-option failed") }
	msg = a.resumeConversationCmd(c)().(conversationResumedMsg)
	if msg.Err == nil {
		t.Error("tagging failed twice, want an error")
	}
	if _, ok := f.sessions[name]; ok {
		t.Errorf("untagged session %s left running", name)
	}

	// A concurrent resume creates the session between the existence
	// check and new-session.
	f.killed = nil
	checks := 0
	resumeTmuxHas = func(_ context.Context, n string) (bool, error) {
		checks++
		_, ok := f.sessions[n]
		return ok && checks > 1, nil
	}
	f.sessions[name] = string(agent.IDCodex)
	resumeTmuxNew = func(_ context.Context, n, _, _, _ string) error {
		return errors.New("tmux new-session: exit status 1 (duplicate session: " + n + ")")
	}
	msg = a.resumeConversationCmd(c)().(conversationResumedMsg)
	if msg.Err != nil || !msg.Existing {
		t.Errorf("lost a create race: %+v, want an attach to the existing session", msg)
	}
	if len(f.killed) != 0 {
		t.Errorf("killed %v, a session this call didn't create", f.killed)
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

// TestResumeConversation_MissingFolderRefused — resuming a conversation
// whose project folder was deleted used to start the session in $HOME,
// where the agent can't find the conversation. The TUI must refuse with
// the folder named, create nothing — and still attach to a session an
// earlier resume left running.
func TestResumeConversation_MissingFolderRefused(t *testing.T) {
	f := installFakeResumeTmux(t)
	a := newAppForTest(t)
	gone := t.TempDir() + "/deleted"
	c := conversations.Conversation{ID: "5f3c1d2e-aaaa-4bbb-8ccc-0123456789ab", Agent: agent.IDCodex, Project: gone}

	msg, ok := a.resumeConversationCmd(c)().(conversationResumedMsg)
	if !ok || msg.Err == nil {
		t.Fatalf("resume in a missing folder = %+v, want an error", msg)
	}
	if len(f.sessions) != 0 {
		t.Errorf("a session was created for a missing folder: %v", f.sessions)
	}

	f.sessions[conversations.ResumeSessionName(c.ID)] = string(agent.IDCodex)
	msg, _ = a.resumeConversationCmd(c)().(conversationResumedMsg)
	if msg.Err != nil || !msg.Existing {
		t.Errorf("a running resume session wasn't reattached: %+v", msg)
	}
}
