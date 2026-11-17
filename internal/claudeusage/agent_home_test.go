package claudeusage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agenthome"
)

func TestMain(m *testing.M) {
	// Fixtures live under a temp $HOME/.claude; an override inherited
	// from the developer's shell would point the walker at their real
	// transcripts instead. Override tests use t.Setenv.
	for _, k := range []string{agenthome.ClaudeEnv, agenthome.CodexEnv, agenthome.CursorEnv} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}

// writeUsageTranscript writes one Claude transcript under
// <root>/<project>/s.jsonl: a prompt 15 minutes ago, answered with the
// given input/output tokens.
func writeUsageTranscript(t *testing.T, root, project string, in, out int) {
	t.Helper()
	dir := filepath.Join(root, project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ts := time.Now().Add(-15 * time.Minute).UTC().Format(time.RFC3339)
	writeJSONL(t, f, map[string]any{"type": "user", "timestamp": ts, "message": map[string]any{"role": "user", "content": "hi"}})
	writeJSONL(t, f, map[string]any{
		"type": "assistant", "timestamp": ts, "requestId": "req_" + project,
		"message": map[string]any{
			"id": "msg_" + project, "role": "assistant", "model": "claude-sonnet-4-6",
			"usage": map[string]any{"input_tokens": in, "output_tokens": out},
		},
	})
}

// TestWalk_HonoursClaudeConfigDir — with CLAUDE_CONFIG_DIR set, Claude
// Code writes its transcripts under $CLAUDE_CONFIG_DIR/projects, but
// the walker read ~/.claude/projects and the dashboard, the daemon's
// /v1/usage and the 5-hour quota bar all showed zero. Both walks now
// read the custom root and only it: tokens left under the default
// root don't count.
func TestWalk_HonoursClaudeConfigDir(t *testing.T) {
	home, claudeRoot := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	writeUsageTranscript(t, filepath.Join(claudeRoot, "projects"), "-w-custom", 100, 20)
	writeUsageTranscript(t, filepath.Join(home, ".claude", "projects"), "-w-default", 7000, 900)

	for name, walk := range bothWalkers() {
		agg, err := walk()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if agg.Total.Input != 100 || agg.Total.Output != 20 {
			t.Errorf("%s: tokens %+v, want 100 in / 20 out from CLAUDE_CONFIG_DIR only", name, agg.Total)
		}
	}
}
