//go:build integration

package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
)

// TestConversations_ClaudeConfigDir runs the CLAUDE_CONFIG_DIR contract
// through real CLI, daemon and TUI processes: a transcript Claude Code
// wrote under $CLAUDE_CONFIG_DIR/projects is listed, counted in usage,
// shown in the TUI and deleted, while a decoy left in ~/.claude/projects
// is never listed or touched.
func TestConversations_ClaudeConfigDir(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)
	proj := filepath.Join(e.Root, "custom-claude-project")
	mkdirAll(t, proj)
	stamp := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	const id = "c1a0de00-1111-4222-8333-444444444444"
	const decoyID = "dec00000-aaaa-4bbb-8ccc-dddddddddddd"

	claudeHome := filepath.Join(e.Home, "claude-work")
	path := filepath.Join(claudeHome, "projects", claudeProjectDir(proj), id+".jsonl")
	var body strings.Builder
	for _, line := range []map[string]any{
		{"type": "user", "cwd": proj, "timestamp": stamp,
			"message": map[string]any{"role": "user", "content": "custom claude root conversation"}},
		{"type": "assistant", "cwd": proj, "timestamp": stamp, "requestId": "req_1",
			"message": map[string]any{"id": "msg_1", "role": "assistant", "model": "claude-sonnet-4-6",
				"usage": map[string]any{"input_tokens": 100, "output_tokens": 20}}},
	} {
		b, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		body.Write(append(b, '\n'))
	}
	writeFile(t, path, body.String())
	decoy := e.writeClaudeTranscript(decoyID, proj, "default root decoy", stamp)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)

	found := false
	for _, c := range e.listConversationsJSON() {
		if c.ID == id {
			found = c.Path == path
		}
		if c.ID == decoyID {
			t.Fatal("CLI listed the ~/.claude/projects decoy despite CLAUDE_CONFIG_DIR")
		}
	}
	if !found {
		t.Fatalf("CLI did not list the CLAUDE_CONFIG_DIR transcript at %s", path)
	}

	e.startDaemon()
	out, stderr, err := e.ccmux("usage", "--json")
	if err != nil {
		t.Fatalf("ccmux usage: %v\nstderr: %s", err, stderr)
	}
	var usage daemon.AgentUsage
	if err := json.Unmarshal([]byte(out), &usage); err != nil {
		t.Fatal(err)
	}
	if !usage.Claude.HasData || usage.Claude.InputTokens != 100 || usage.Claude.OutputTokens != 20 {
		t.Fatalf("daemon/CLI usage ignored CLAUDE_CONFIG_DIR: %+v", usage.Claude)
	}

	d := newTUIDriver(t, e, 40, 140)
	d.WaitFor("Sessions")
	d.Send("3")
	d.WaitFor("custom claude root conversation")
	d.Quit()

	if _, stderr, err := e.ccmux("delete-conversation", id, "--force"); err != nil {
		t.Fatalf("delete custom-root transcript: %v\nstderr: %s", err, stderr)
	}
	if exists(path) || !exists(decoy) {
		t.Fatal("deletion did not remove only the CLAUDE_CONFIG_DIR transcript")
	}
}
