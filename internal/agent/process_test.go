package agent

import "testing"

func TestInForeground(t *testing.T) {
	idle := readPaneFixture(t, "claude_v2_idle.txt")
	for _, tc := range []struct {
		name, command, pane, title string
		want                       ID // "" for none
	}{
		{"native claude", "claude", "", "", IDClaude},
		{"native installer on macOS", "2.1.281", "", "", IDClaude},
		{"npm claude", "node", idle, "✳ Claude Code", IDClaude},
		{"some other node program", "node", "Welcome to Node.js v22.22.3.\n> ", "", ""},
		{"a shell at its prompt", "zsh", idle, "✳ Claude Code", ""}, // a dead Claude's screen and title
		{"codex", "codex", "", "", IDCodex},
		{"nothing", "", "", "", ""},
	} {
		id, ok := InForeground(tc.command, tc.pane, tc.title)
		if id != tc.want || ok != (tc.want != "") {
			t.Errorf("%s: InForeground = %q, %v; want %q", tc.name, id, ok, tc.want)
		}
	}
}

func TestForProcess(t *testing.T) {
	// Every registered agent is recognised by its binary name.
	for _, a := range All() {
		if id, ok := ForProcess(a.Binary()); !ok || id != a.ID() {
			t.Errorf("ForProcess(%q) = %q, %v; want %q", a.Binary(), id, ok, a.ID())
		}
	}
	for name, want := range map[string]ID{
		"2.1.281":        IDClaude, // the native installer's versioned binary
		"2.1.281-beta.1": IDClaude,
		"10.0.0":         IDClaude,
		"claude":         IDClaude,
		"codex":          IDCodex,
	} {
		if id, ok := ForProcess(name); !ok || id != want {
			t.Errorf("ForProcess(%q) = %q, %v; want %q", name, id, ok, want)
		}
	}
	for _, name := range []string{
		"", "zsh", "bash", "-zsh", "node", "python3", "tail", "vim", "less",
		"2.1", "v2.1.281", "2.1.281x", "claude-code", "Claude",
	} {
		if id, ok := ForProcess(name); ok {
			t.Errorf("ForProcess(%q) = %q, want no agent", name, id)
		}
	}
}
