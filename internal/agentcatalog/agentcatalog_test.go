package agentcatalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

func has(cmds []agent.AgentCommand, name string) (agent.AgentCommand, bool) {
	for _, c := range cmds {
		if c.Name == name {
			return c, true
		}
	}
	return agent.AgentCommand{}, false
}

// writeClaudeCommand drops a user command alias into a temp ~/.claude.
func writeClaudeCommand(t *testing.T, name, body string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".claude")
	t.Setenv("HOME", filepath.Dir(dir))
	cmdsDir := filepath.Join(dir, "commands")
	if err := os.MkdirAll(cmdsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdsDir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestResolve_ClaudeMergesUserCommands proves the headline behavior: a
// Claude session's catalog includes both the built-in slash-commands
// and the host user's own command aliases.
func TestResolve_ClaudeMergesUserCommands(t *testing.T) {
	writeClaudeCommand(t, "deploy", "# Deploy\nShip the current branch to prod")

	cmds := Resolve(agent.Claude{})
	if _, ok := has(cmds, "/model"); !ok {
		t.Errorf("resolved Claude catalog should keep the built-ins (/model)")
	}
	deploy, ok := has(cmds, "/deploy")
	if !ok {
		t.Fatalf("resolved Claude catalog should include the user command /deploy")
	}
	if deploy.Source != "command" {
		t.Errorf("/deploy Source = %q, want command", deploy.Source)
	}
	if deploy.Description == "" {
		t.Errorf("/deploy should carry its description from the file")
	}
}

// TestResolve_NonClaudeNoUserMerge proves the user layer is Claude-only:
// a Codex session resolves to exactly Codex's built-ins even when a
// ~/.claude command exists on the host.
func TestResolve_NonClaudeNoUserMerge(t *testing.T) {
	writeClaudeCommand(t, "deploy", "# Deploy\nShip it")

	cmds := Resolve(agent.Codex{})
	if _, ok := has(cmds, "/deploy"); ok {
		t.Errorf("Codex catalog must not pull in ~/.claude commands")
	}
	if _, ok := has(cmds, "/undo"); !ok {
		t.Errorf("Codex catalog should be its built-ins (/undo)")
	}
	if len(cmds) != len(agent.BuiltinCommands(agent.Codex{})) {
		t.Errorf("Codex resolved (%d) should equal built-ins (%d)", len(cmds), len(agent.BuiltinCommands(agent.Codex{})))
	}
}

func TestResolveByID(t *testing.T) {
	writeClaudeCommand(t, "deploy", "# Deploy\nShip it")
	// Empty/unknown id → default agent (Claude), which merges user cmds.
	if _, ok := has(ResolveByID(""), "/deploy"); !ok {
		t.Errorf("empty id should resolve to Claude and include /deploy")
	}
	if _, ok := has(ResolveByID("codex"), "/diff"); !ok {
		t.Errorf("codex id should resolve to Codex built-ins")
	}
}
