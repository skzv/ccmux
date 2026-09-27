package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
)

// fakeResumeTmux swaps ensureResumeSession's tmux seams for an
// in-memory session set, so the test never touches a real tmux server.
func fakeResumeTmux(t *testing.T) map[string]bool {
	t.Helper()
	sessions := map[string]bool{}
	origNew, origHas, origTag, origKill := resumeTmuxNew, resumeTmuxHas, resumeTmuxSetAgent, resumeTmuxKill
	t.Cleanup(func() {
		resumeTmuxNew, resumeTmuxHas, resumeTmuxSetAgent, resumeTmuxKill = origNew, origHas, origTag, origKill
	})
	resumeTmuxNew = func(_ context.Context, name, _, _ string) error {
		if sessions[name] {
			return errors.New("duplicate session: " + name)
		}
		sessions[name] = true
		return nil
	}
	resumeTmuxHas = func(_ context.Context, name string) (bool, error) { return sessions[name], nil }
	resumeTmuxSetAgent = func(context.Context, string, string) error { return nil }
	resumeTmuxKill = func(_ context.Context, name string) error {
		delete(sessions, name)
		return nil
	}
	return sessions
}

// TestEnsureResumeSession_UUIDv7NeighboursDistinct — regression:
// `ccmux resume` named the session after the ID's first 8 characters,
// so two Codex (UUIDv7) conversations started within ~65s shared a
// session and resuming the second attached to the first. The name must
// come from the helper shared with the TUI and differ per conversation.
func TestEnsureResumeSession_UUIDv7NeighboursDistinct(t *testing.T) {
	fakeResumeTmux(t)
	ctx := context.Background()
	c1 := conversations.Conversation{ID: "0198a3c2-1b2c-7d3e-8f9a-0b1c2d3e4f5a", Agent: agent.IDCodex}
	c2 := conversations.Conversation{ID: "0198a3c2-9e8d-7c6b-a5f4-e3d2c1b0a998", Agent: agent.IDCodex}

	n1, existed1, err := ensureResumeSession(ctx, c1, "codex resume x")
	if err != nil || existed1 {
		t.Fatalf("first: name=%q existed=%v err=%v", n1, existed1, err)
	}
	n2, existed2, err := ensureResumeSession(ctx, c2, "codex resume y")
	if err != nil {
		t.Fatal(err)
	}
	if n1 == n2 || existed2 {
		t.Fatalf("distinct conversations resolved to the same session %q (existed=%v)", n1, existed2)
	}
	if n1 != conversations.ResumeSessionName(c1.ID) || n2 != conversations.ResumeSessionName(c2.ID) {
		t.Errorf("CLI names %q/%q must match conversations.ResumeSessionName (shared with the TUI)", n1, n2)
	}

	// Resuming c1 again attaches to its existing session.
	again, existed, err := ensureResumeSession(ctx, c1, "codex resume x")
	if err != nil || !existed || again != n1 {
		t.Errorf("re-resume: name=%q existed=%v err=%v, want existing %q", again, existed, err, n1)
	}
}

// TestEnsureResumeSession_TagFailureKillsNewSession — when tagging the
// new session with its agent failed, `ccmux resume` returned the error
// but left the untagged session running, and the next resume of the
// same conversation silently attached to it. The session this call
// created must be killed (as the TUI does); one that was already
// running must not be.
func TestEnsureResumeSession_TagFailureKillsNewSession(t *testing.T) {
	sessions := fakeResumeTmux(t)
	resumeTmuxSetAgent = func(context.Context, string, string) error { return errors.New("set-option failed") }
	ctx := context.Background()
	c := conversations.Conversation{ID: "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b", Agent: agent.IDClaude}
	name := conversations.ResumeSessionName(c.ID)

	if _, _, err := ensureResumeSession(ctx, c, "claude --resume x"); err == nil {
		t.Fatal("tagging failed, want an error")
	}
	if sessions[name] {
		t.Errorf("session %s left running after its agent tag failed", name)
	}

	sessions[name] = true // resumed earlier and still running
	if _, _, err := ensureResumeSession(ctx, c, "claude --resume x"); err == nil {
		t.Fatal("tagging failed, want an error")
	}
	if !sessions[name] {
		t.Errorf("killed %s, a session this call didn't create", name)
	}
}
