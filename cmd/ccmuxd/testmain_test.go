package main

import (
	"os"
	"testing"

	"github.com/skzv/ccmux/internal/agenthome"
)

// TestMain clears the agent-home overrides before any test runs. The
// usage and conversation handler tests write fixtures under a temp
// $HOME/.claude; one of these variables inherited from the developer's
// shell would point the daemon's walkers at their real transcripts
// instead. Tests that need an override set it with t.Setenv.
func TestMain(m *testing.M) {
	for _, k := range []string{agenthome.ClaudeEnv, agenthome.CodexEnv, agenthome.CursorEnv} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
