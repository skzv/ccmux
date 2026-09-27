package cmd

import (
	"strings"
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
	got, err := pickByID(fixture(), "antigravity-1d")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "antigravity-1d" {
		t.Errorf("got id=%q, want antigravity-1d", got.ID)
	}
	if got.Agent != agent.IDAntigravity {
		t.Errorf("got agent=%q, want antigravity", got.Agent)
	}
}

// TestPickByID_MissingReturnsZero — when no row matches, return the
// zero Conversation and an error pointing at list-conversations.
func TestPickByID_MissingReturnsZero(t *testing.T) {
	got, err := pickByID(fixture(), "no-such-id")
	if got.ID != "" {
		t.Errorf("got id=%q, want empty (not-found sentinel)", got.ID)
	}
	if err == nil || !strings.Contains(err.Error(), "list-conversations") {
		t.Errorf("err = %v, want a not-found error pointing at list-conversations", err)
	}
}

// TestPickByID_AcceptsTableFormAndUniquePrefix — list-conversations
// prints IDs cut to 11 characters plus "…"; pasting that (or any other
// unique prefix) must resolve, while an exact ID still wins over a
// longer ID it happens to prefix.
func TestPickByID_AcceptsTableFormAndUniquePrefix(t *testing.T) {
	list := []conversations.Conversation{
		{ID: "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b", Agent: agent.IDClaude},
		{ID: "9e8d7c6b-5a4f-4e3d-8c2b-1a0f9e8d7c6b", Agent: agent.IDCodex},
		{ID: "abc", Agent: agent.IDPi},
		{ID: "abcdef", Agent: agent.IDPi},
	}
	cases := map[string]string{
		"3f2a1b4c-5d…":                           "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b", // the table form
		"3f2a1b4c-5d...":                         "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b", // ASCII ellipsis
		"9e8d":                                   "9e8d7c6b-5a4f-4e3d-8c2b-1a0f9e8d7c6b",
		" 9e8d7c6b-5a4f-4e3d-8c2b-1a0f9e8d7c6b ": "9e8d7c6b-5a4f-4e3d-8c2b-1a0f9e8d7c6b",
		"abc":                                    "abc", // exact beats the longer "abcdef"
		"abcd":                                   "abcdef",
	}
	for in, want := range cases {
		got, err := pickByID(list, in)
		if err != nil || got.ID != want {
			t.Errorf("pickByID(%q) = %q, %v; want %q", in, got.ID, err, want)
		}
	}
	for _, in := range []string{"", "…", "zzz", "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b-extra"} {
		if got, err := pickByID(list, in); err == nil {
			t.Errorf("pickByID(%q) = %q, want an error", in, got.ID)
		}
	}
}

// TestPickByID_AmbiguousPrefixListsCandidates — a prefix several
// conversations share must fail and name every candidate, never pick
// one (delete-conversation would remove the wrong transcript).
func TestPickByID_AmbiguousPrefixListsCandidates(t *testing.T) {
	list := []conversations.Conversation{
		{ID: "0198a3c2-1b2c-7d3e-8f9a-0b1c2d3e4f5a", Agent: agent.IDCodex, Preview: "first"},
		{ID: "0198a3c2-9e8d-7c6b-a5f4-e3d2c1b0a998", Agent: agent.IDCodex, Preview: "second"},
		{ID: "7a7a7a7a-0000-4000-8000-000000000000", Agent: agent.IDClaude},
	}
	got, err := pickByID(list, "0198a3c2-…")
	if err == nil {
		t.Fatalf("ambiguous prefix resolved to %q, want an error", got.ID)
	}
	for _, want := range []string{"ambiguous", list[0].ID, list[1].ID, "first", "second"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), list[2].ID) {
		t.Errorf("error lists a conversation the prefix doesn't match:\n%v", err)
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
		{[]string{"/Users/me/Tools With Spaces/claude", "--resume", "abc"}, "'/Users/me/Tools With Spaces/claude' --resume abc"},
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
