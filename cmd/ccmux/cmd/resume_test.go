package cmd

import (
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
)

// fixture builds a stable three-conversation slice spanning all three
// agents. Order matches what `conversations.All()` returns (sorted by
// recency desc) so picker helpers see the same shape they would in
// production.
func fixture() []conversations.Conversation {
	now := time.Now()
	return []conversations.Conversation{
		{ID: "claude-now", Agent: agent.IDClaude, LastActivity: now},
		{ID: "codex-1h", Agent: agent.IDCodex, LastActivity: now.Add(-1 * time.Hour)},
		{ID: "antigravity-1d", Agent: agent.IDAntigravity, LastActivity: now.Add(-24 * time.Hour)},
		{ID: "claude-2d", Agent: agent.IDClaude, LastActivity: now.Add(-48 * time.Hour)},
	}
}

// TestPickByID_FindsExact — the explicit `ccmux resume <id>` path
// must locate the row even when it's not the most recent. Otherwise
// the user typing a specific id would always get the latest.
func TestPickByID_FindsExact(t *testing.T) {
	got := pickByID(fixture(), "antigravity-1d")
	if got.ID != "antigravity-1d" {
		t.Errorf("got id=%q, want antigravity-1d", got.ID)
	}
	if got.Agent != agent.IDAntigravity {
		t.Errorf("got agent=%q, want antigravity", got.Agent)
	}
}

// TestPickByID_MissingReturnsZero — when no row matches, return the
// zero Conversation so the caller can detect "not found" by checking
// .ID == "". The CLI then surfaces a friendly error.
func TestPickByID_MissingReturnsZero(t *testing.T) {
	got := pickByID(fixture(), "no-such-id")
	if got.ID != "" {
		t.Errorf("got id=%q, want empty (not-found sentinel)", got.ID)
	}
}

// TestPickMostRecentByAgent_RespectsRecencyOrder — `ccmux resume
// --agent claude` must pick the MOST RECENT Claude conversation, not
// any Claude conversation. The fixture has two Claude rows at
// different timestamps; we must get claude-now (newer) not claude-2d.
func TestPickMostRecentByAgent_RespectsRecencyOrder(t *testing.T) {
	got := pickMostRecentByAgent(fixture(), agent.IDClaude)
	if got.ID != "claude-now" {
		t.Errorf("got id=%q, want claude-now (most recent Claude)", got.ID)
	}
}

// TestPickMostRecentByAgent_NoMatchReturnsZero — fresh install without
// any past Codex sessions should hit this branch.
func TestPickMostRecentByAgent_NoMatchReturnsZero(t *testing.T) {
	noCodex := []conversations.Conversation{
		{ID: "claude-x", Agent: agent.IDClaude, LastActivity: time.Now()},
	}
	got := pickMostRecentByAgent(noCodex, agent.IDCodex)
	if got.ID != "" {
		t.Errorf("got id=%q, want empty", got.ID)
	}
}

// TestJoinArgs_ShapeMatchesSpaceSeparation — agent argv joined with a
// space matches what tmux's new-session expects in the cmdline slot.
// Empty list = empty string (no quirky single-space prefix).
func TestJoinArgs_ShapeMatchesSpaceSeparation(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"claude", "--resume", "abc"}, "claude --resume abc"},
		{[]string{"codex", "resume", "uuid"}, "codex resume uuid"},
		{[]string{"agy", "--conversation", "x"}, "agy --conversation x"},
		{[]string{"single"}, "single"},
		{nil, ""},
		{[]string{}, ""},
	}
	for _, tc := range cases {
		if got := joinArgs(tc.in); got != tc.want {
			t.Errorf("joinArgs(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
