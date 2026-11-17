package usage

import (
	"os"
	"testing"

	"github.com/skzv/ccmux/internal/agenthome"
)

// TestMain clears the agent-home overrides so walkers run against the
// temp $HOME each test sets, never a developer's real transcripts
// through an inherited CLAUDE_CONFIG_DIR / CODEX_HOME / CURSOR_HOME.
func TestMain(m *testing.M) {
	for _, k := range []string{agenthome.ClaudeEnv, agenthome.CodexEnv, agenthome.CursorEnv} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
