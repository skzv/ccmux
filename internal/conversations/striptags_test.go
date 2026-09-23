package conversations

import (
	"path/filepath"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

// TestStripXMLTags — regression: the old depth scanner dropped
// everything after a bare '<', so "is a < b true" became "is a",
// "Vec<String>" became "Vec", and "<- this arrow in R" vanished (the
// message was then skipped as empty). Only well-formed wrapper tags are
// stripped now.
func TestStripXMLTags(t *testing.T) {
	cases := []struct{ in, want string }{
		// Prose and code keep their angle brackets.
		{"is a < b true", "is a < b true"},
		{"<- this arrow in R", "<- this arrow in R"},
		{"x <- c(1, 2)", "x <- c(1, 2)"},
		{"Vec<String>", "Vec<String>"},
		{"use Vec<String> here", "use Vec<String> here"},
		{"Map<String, List<Integer>>", "Map<String, List<Integer>>"},
		{"Promise<void> and Array<TArg>", "Promise<void> and Array<TArg>"},
		{"a > b and c >= d", "a > b and c >= d"},
		{"if x<y and y>z", "if x<y and y>z"},
		{"1 << 3", "1 << 3"},
		{"<<EOF heredoc", "<<EOF heredoc"},
		{"-> arrow", "-> arrow"},
		{"<3 thanks", "<3 thanks"},
		// Wrapper tags Claude Code / Codex / Cursor inject are stripped.
		{"<system-reminder>be terse</system-reminder>", "be terse"},
		{"<command-message>review</command-message>", "review"},
		{"<user_query>build it</user_query>", "build it"},
		{"<ide_opened_file>The user opened a.go</ide_opened_file> fix it", "The user opened a.go fix it"},
		{`<file path="a.go" line="3">body</file>`, "body"},
		{"line<br/>break", "linebreak"},
		{"text<system-reminder>note</system-reminder>", "textnote"},
		{"Vec<String> <system-reminder>x</system-reminder>", "Vec<String> x"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := stripXMLTags(tc.in); got != tc.want {
			t.Errorf("stripXMLTags(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRecentMessages_KeepsAngleBracketProse — end to end: a user turn
// that is only "<- this arrow in R" used to be dropped from the
// transcript modal, and "is a < b true" was cut to "is a".
func TestRecentMessages_KeepsAngleBracketProse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abc.jsonl")
	writeFile(t, path,
		`{"type":"user","message":{"role":"user","content":"<- this arrow in R"},"timestamp":"2026-04-30T10:00:00Z"}`+"\n"+
			`{"type":"assistant","message":{"role":"assistant","content":"assignment"},"timestamp":"2026-04-30T10:00:01Z"}`+"\n"+
			`{"type":"user","message":{"role":"user","content":"is a < b true for Vec<String>?"},"timestamp":"2026-04-30T10:00:02Z"}`+"\n",
	)
	c := Conversation{Agent: agent.IDClaude, Path: path}
	got, err := RecentMessages(c, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(got), got)
	}
	if got[0].Content != "<- this arrow in R" || got[2].Content != "is a < b true for Vec<String>?" {
		t.Errorf("user turns mangled: %q / %q", got[0].Content, got[2].Content)
	}
	if p := truncatedPreview("is a < b true"); p != "is a < b true" {
		t.Errorf("preview = %q", p)
	}
}
