package conversations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

// hasControl reports whether s still carries an escape or other
// control character (newline and tab excepted).
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f)
	})
}

// TestConversationText_StripsTerminalEscapes — regression: previews and
// transcript messages were rendered with raw escape sequences from the
// transcript, so an OSC 52 in a prompt could rewrite the clipboard and
// ANSI colors broke the list rows.
func TestConversationText_StripsTerminalEscapes(t *testing.T) {
	home := t.TempDir()
	const osc52 = `\u001b]52;c;cm0gLXJmIH4=\u0007`
	writeFile(t, filepath.Join(home, ".claude/projects/-p/s1.jsonl"),
		`{"type":"user","message":{"role":"user","content":"please `+osc52+`fix \u001b[1;31mthe\u001b[0m bug"},"timestamp":"2026-05-01T10:00:00Z"}`+"\n"+
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done \u001b]0;pwned\u0007\u001b[2J!"}]},"timestamp":"2026-05-01T10:00:01Z"}`+"\n")
	writeFile(t, filepath.Join(home, ".codex/sessions/2026/05/01/rollout-2026-05-01T10-00-00-00000000-0000-0000-0000-000000000001.jsonl"),
		`{"timestamp":"2026-05-01T10:00:00Z","type":"session_meta","payload":{"id":"x","originator":"codex-tui","source":"cli","cwd":"/p"}}`+"\n"+
			`{"timestamp":"2026-05-01T10:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi `+osc52+`there"}]}}`+"\n"+
			`{"timestamp":"2026-05-01T10:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"\u001b[32mok\u001b[0m"}]}}`+"\n")

	all, err := All(Options{HomeDir: home})
	if err != nil || len(all) != 2 {
		t.Fatalf("All = %d rows, %v", len(all), err)
	}
	wantPreview := map[agent.ID]string{agent.IDClaude: "please fix the bug", agent.IDCodex: "hi there"}
	for _, c := range all {
		if c.Preview != wantPreview[c.Agent] {
			t.Errorf("%s preview = %q, want %q", c.Agent, c.Preview, wantPreview[c.Agent])
		}
		msgs, err := RecentMessages(c, 10)
		if err != nil || len(msgs) != 2 {
			t.Fatalf("%s RecentMessages = %+v, %v", c.Agent, msgs, err)
		}
		for _, m := range msgs {
			if hasControl(m.Content) {
				t.Errorf("%s %s message kept control sequences: %q", c.Agent, m.Role, m.Content)
			}
		}
	}
}

// TestMuseText_StripsTerminalEscapes covers the Muse path, which builds
// previews and messages without cleanPromptText.
func TestMuseText_StripsTerminalEscapes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	id := "01a0828e-037e-7c32-bd87-87b6e25b70fd"
	path := filepath.Join(home, "data/muse/sessions/2026/09/08", id, "session.jsonl")
	data, err := os.ReadFile("../muse/testdata/native-session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.ReplaceAll(string(data), "ccmux format probe", `ccmux \u001b]52;c;QQ==\u0007format\u001b[0m probe`))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	all, err := ListMuse(home)
	if err != nil || len(all) != 1 {
		t.Fatalf("ListMuse = %v, %v", all, err)
	}
	if all[0].Preview != "ccmux format probe" {
		t.Errorf("Muse preview = %q", all[0].Preview)
	}
	msgs, err := RecentMessages(all[0], 10)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("RecentMessages = %v, %v", msgs, err)
	}
	for _, m := range msgs {
		if hasControl(m.Content) {
			t.Errorf("Muse %s message kept control sequences: %q", m.Role, m.Content)
		}
	}
}
