package conversations

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/claudeconfig"
	"github.com/skzv/ccmux/internal/cursorconfig"
)

// TestClaudeConfigDir_ListReadAndDelete — Claude Code keeps its
// transcripts under $CLAUDE_CONFIG_DIR/projects when that is set, and
// ccmux's config tab already followed it, but listing and deletion
// still read ~/.claude/projects: a user with CLAUDE_CONFIG_DIR set saw
// no conversations, and delete targeted the wrong tree. With the
// variable set, the custom root is the only Claude root: its
// transcripts are listed, read and deleted (session directory
// included), and a transcript left under the default root is neither
// listed nor deletable.
func TestClaudeConfigDir_ListReadAndDelete(t *testing.T) {
	home, claudeRoot := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)

	const id = "0f5c2a8e-1b2c-4d3e-8f90-a1b2c3d4e5f6"
	const decoyID = "11111111-2222-4333-8444-555555555555"
	projectDir := filepath.Join(claudeRoot, "projects", "-Users-me-Projects-app")
	path := filepath.Join(projectDir, id+".jsonl")
	writeFile(t, path,
		`{"type":"user","cwd":"/Users/me/Projects/app","message":{"role":"user","content":"custom root prompt"},"timestamp":"2026-04-30T10:00:00Z"}`+"\n"+
			`{"type":"assistant","message":{"role":"assistant","content":"ok"},"timestamp":"2026-04-30T10:01:00Z"}`+"\n")
	toolResult := filepath.Join(projectDir, id, "tool-results", "toolu_01.txt")
	writeFile(t, toolResult, "file contents the session read")
	decoy := filepath.Join(home, ".claude", "projects", "-Users-me-Projects-app", decoyID+".jsonl")
	writeFile(t, decoy,
		`{"type":"user","message":{"role":"user","content":"default root decoy"},"timestamp":"2026-04-30T10:00:00Z"}`+"\n")

	if got := (agent.Claude{}).TranscriptsRoot(home); got != filepath.Join(claudeRoot, "projects") {
		t.Errorf("agent transcripts root = %s, want %s", got, filepath.Join(claudeRoot, "projects"))
	}
	if got := (agent.Claude{}).ConfigRoot(home); got != claudeRoot {
		t.Errorf("agent config root = %s, want %s", got, claudeRoot)
	}
	if locs, err := claudeconfig.Paths(); err != nil || locs.Root != claudeRoot {
		t.Errorf("claudeconfig root = %+v, %v; want %s", locs, err, claudeRoot)
	}

	all, err := All(Options{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	var found *Conversation
	for i, c := range all {
		switch c.ID {
		case id:
			found = &all[i]
		case decoyID:
			t.Errorf("listed the default-root transcript %s despite CLAUDE_CONFIG_DIR", c.Path)
		}
	}
	if found == nil {
		t.Fatalf("CLAUDE_CONFIG_DIR transcript not listed; got %+v", all)
	}
	if found.Path != path || found.Preview != "custom root prompt" {
		t.Errorf("listed row = %+v, want path %s and the first prompt", *found, path)
	}
	if n, err := CountMessages(*found); err != nil || n != 2 {
		t.Errorf("CountMessages = %d, %v; want 2", n, err)
	}

	if err := Delete(Conversation{ID: decoyID, Agent: agent.IDClaude, Path: decoy}); err == nil {
		t.Error("deleted a transcript under ~/.claude/projects although CLAUDE_CONFIG_DIR moves the root")
	}
	if _, err := os.Stat(decoy); err != nil {
		t.Errorf("default-root transcript was touched: %v", err)
	}

	if err := Delete(*found); err != nil {
		t.Fatalf("Delete custom-root conversation: %v", err)
	}
	for _, p := range []string{path, filepath.Join(projectDir, id)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived Delete (stat err %v)", p, err)
		}
	}
}

// TestClaudeConfigDir_DeleteGuardResolvesSymlinks — the containment
// guard is anchored to the resolved custom root: a project directory
// inside it that is a symlink to somewhere else can't be used to
// delete outside it, while a custom root that is itself a symlink
// (a common way to relocate ~/.claude) still works.
func TestClaudeConfigDir_DeleteGuardResolvesSymlinks(t *testing.T) {
	home, real := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	link := filepath.Join(home, "claude-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", link)

	inside := filepath.Join(link, "projects", "-p", "abc.jsonl")
	writeFile(t, inside, "{}\n")
	if err := Delete(Conversation{ID: "abc", Agent: agent.IDClaude, Path: inside}); err != nil {
		t.Fatalf("Delete under a symlinked CLAUDE_CONFIG_DIR: %v", err)
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Errorf("transcript survived Delete (stat err %v)", err)
	}

	outside := filepath.Join(home, "elsewhere", "keep.jsonl")
	writeFile(t, outside, "{}\n")
	escape := filepath.Join(link, "projects", "escape")
	if err := os.Symlink(filepath.Dir(outside), escape); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := Delete(Conversation{ID: "keep", Agent: agent.IDClaude, Path: filepath.Join(escape, "keep.jsonl")}); err == nil {
		t.Error("Delete followed a symlinked project directory out of CLAUDE_CONFIG_DIR")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("file outside the root was removed: %v", err)
	}
}

// TestCursorHome_ListReadAndDelete — the Cursor config tab honoured
// $CURSOR_HOME, but the conversation listing and deletion read
// ~/.cursor/projects regardless. They now share one root.
func TestCursorHome_ListReadAndDelete(t *testing.T) {
	home, cursorRoot := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CURSOR_HOME", cursorRoot)

	line := `{"role":"user","message":{"content":[{"type":"text","text":"%s"}]},"timestamp":"2026-05-24T10:00:00Z"}` + "\n"
	path := filepath.Join(cursorRoot, "projects", "Users-me-Projects-app", "agent-transcripts", "cur-1", "cur-1.jsonl")
	writeFile(t, path, fmt.Sprintf(line, "custom cursor prompt"))
	decoy := filepath.Join(home, ".cursor", "projects", "Users-me-Projects-app", "agent-transcripts", "cur-2", "cur-2.jsonl")
	writeFile(t, decoy, fmt.Sprintf(line, "default root decoy"))

	if got := (agent.Cursor{}).TranscriptsRoot(home); got != filepath.Join(cursorRoot, "projects") {
		t.Errorf("agent transcripts root = %s, want %s", got, filepath.Join(cursorRoot, "projects"))
	}
	if locs, err := cursorconfig.Paths(); err != nil || locs.Root != cursorRoot {
		t.Errorf("cursorconfig root = %+v, %v; want %s", locs, err, cursorRoot)
	}

	got, err := ListCursor(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "cur-1" || got[0].Path != path || got[0].Preview != "custom cursor prompt" {
		t.Fatalf("ListCursor = %+v, want only cur-1 from CURSOR_HOME", got)
	}

	if err := Delete(Conversation{ID: "cur-2", Agent: agent.IDCursor, Path: decoy}); err == nil {
		t.Error("deleted a transcript under ~/.cursor/projects although CURSOR_HOME moves the root")
	}
	if _, err := os.Stat(decoy); err != nil {
		t.Errorf("default-root transcript was touched: %v", err)
	}
	if err := Delete(got[0]); err != nil {
		t.Fatalf("Delete custom-root conversation: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("transcript survived Delete (stat err %v)", err)
	}
}

// TestAgentHome_RelativeOverrideIgnored — a relative override would
// resolve against whatever directory ccmux runs in. It is ignored
// everywhere alike, so the config tab (which used to take it verbatim)
// and listing and deletion all stay anchored to the default root.
func TestAgentHome_RelativeOverrideIgnored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(home)
	t.Setenv("CLAUDE_CONFIG_DIR", "relative-claude")
	if locs, err := claudeconfig.Paths(); err != nil || locs.Root != filepath.Join(home, ".claude") {
		t.Errorf("claudeconfig root = %+v, %v; want the default %s", locs, err, filepath.Join(home, ".claude"))
	}
	path := filepath.Join(home, ".claude", "projects", "-p", "abc.jsonl")
	writeFile(t, path, `{"type":"user","message":{"role":"user","content":"hi"},"timestamp":"2026-04-30T10:00:00Z"}`+"\n")
	stray := filepath.Join(home, "relative-claude", "projects", "-p", "def.jsonl")
	writeFile(t, stray, `{"type":"user","message":{"role":"user","content":"hi"},"timestamp":"2026-04-30T10:00:00Z"}`+"\n")

	got, err := ListClaude(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != path {
		t.Fatalf("ListClaude = %+v, want only the default-root transcript", got)
	}
	if err := Delete(Conversation{ID: "def", Agent: agent.IDClaude, Path: stray}); err == nil {
		t.Error("deleted under a relative CLAUDE_CONFIG_DIR")
	}
}
