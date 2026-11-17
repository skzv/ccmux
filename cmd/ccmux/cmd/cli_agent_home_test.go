//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/conversations"
)

// TestCLI_ClaudeConfigDir_ListResumeDelete — with CLAUDE_CONFIG_DIR
// set, Claude Code writes its transcripts under
// $CLAUDE_CONFIG_DIR/projects, but `ccmux list-conversations` read
// ~/.claude/projects and showed nothing, and resume/delete-conversation
// couldn't find the id. All three now use the custom root, and a
// transcript left in the default root is neither listed nor deleted.
func TestCLI_ClaudeConfigDir_ListResumeDelete(t *testing.T) {
	e := newCLIEnv(t)
	const id = "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b"
	const decoyID = "9e8d7c6b-5a49-4382-9170-6f5e4d3c2b1a"
	decoy := e.seedClaudeTranscript(decoyID, "default root decoy")

	// Seed the real transcript the same way, then move it into a custom
	// config dir: from here on only CLAUDE_CONFIG_DIR leads to it.
	seeded := e.seedClaudeTranscript(id, "fix the login redirect")
	custom := filepath.Join(e.home, "claude-work")
	rel, err := filepath.Rel(filepath.Join(e.home, ".claude"), seeded)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(custom, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(seeded, path); err != nil {
		t.Fatal(err)
	}
	e.env["CLAUDE_CONFIG_DIR"] = custom

	list := e.run("", "list-conversations", "--json")
	if list.code != 0 {
		t.Fatalf("list-conversations exit %d\nstderr: %s", list.code, list.stderr)
	}
	if !strings.Contains(list.stdout, id) || !strings.Contains(list.stdout, path) {
		t.Errorf("list-conversations should show %s at %s:\n%s", id, path, list.stdout)
	}
	if strings.Contains(list.stdout, decoyID) {
		t.Errorf("list-conversations listed the ~/.claude/projects decoy despite CLAUDE_CONFIG_DIR:\n%s", list.stdout)
	}

	res := e.run("", "resume", id)
	if res.code != 0 {
		t.Fatalf("resume exit %d\nstderr: %s", res.code, res.stderr)
	}
	if !hasCall(e.tmuxCallsWith("new-session"), "-s", conversations.ResumeSessionName(id)) {
		t.Errorf("resume should start the conversation's session; tmux calls:\n%s", strings.Join(e.tmuxCalls(), "\n"))
	}

	res = e.run("", "delete-conversation", "--force", id)
	if res.code != 0 {
		t.Fatalf("delete-conversation exit %d\nstderr: %s", res.code, res.stderr)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("transcript still under CLAUDE_CONFIG_DIR after delete-conversation (stat err %v)", err)
	}
	if res := e.run("", "delete-conversation", "--force", decoyID); res.code == 0 {
		t.Errorf("delete-conversation found the ~/.claude/projects decoy despite CLAUDE_CONFIG_DIR")
	}
	if _, err := os.Stat(decoy); err != nil {
		t.Errorf("default-root transcript was touched: %v", err)
	}
}
