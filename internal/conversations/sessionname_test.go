package conversations

import (
	"strings"
	"testing"
)

// TestResumeSessionName_UUIDv7SharedPrefixDistinct — regression: the
// resume session name kept only the first 8 characters of the ID.
// Codex IDs are UUIDv7 (timestamp-first), so two conversations started
// within ~65s produced the same name and resuming the second attached
// to the first one's session.
func TestResumeSessionName_UUIDv7SharedPrefixDistinct(t *testing.T) {
	a := "0198a3c2-1b2c-7d3e-8f9a-0b1c2d3e4f5a"
	b := "0198a3c2-9e8d-7c6b-a5f4-e3d2c1b0a998" // same 48-bit ms prefix window
	na, nb := ResumeSessionName(a), ResumeSessionName(b)
	if na == nb {
		t.Fatalf("conversations %s and %s share session name %q", a, b, na)
	}
	for _, n := range []string{na, nb} {
		if !strings.HasPrefix(n, "c-resume-0198a3c2-") {
			t.Errorf("name %q should keep the c-resume-<short-id> prefix", n)
		}
	}
}

// TestResumeSessionName_StableAndTmuxSafe — the same ID always maps to
// the same name (so a second resume finds the first session), and
// characters tmux rewrites in session names never appear.
func TestResumeSessionName_StableAndTmuxSafe(t *testing.T) {
	id := "chat.v2:abc"
	if ResumeSessionName(id) != ResumeSessionName(id) {
		t.Fatal("ResumeSessionName is not deterministic")
	}
	if ResumeSessionName(id) != ResumeSessionName("  "+id+" ") {
		t.Error("surrounding whitespace should not change the name")
	}
	n := ResumeSessionName(id)
	if strings.ContainsAny(n, ".: ") {
		t.Errorf("name %q contains characters tmux rewrites", n)
	}
	if ResumeSessionName("short") == ResumeSessionName("short2") {
		t.Error("distinct short IDs collided")
	}
}
