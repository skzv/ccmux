package conversations

import (
	"os"
	"testing"

	"github.com/skzv/ccmux/internal/agenthome"
)

func TestMain(m *testing.M) {
	// Default-root fixtures must never discover or delete real
	// transcripts through an inherited override. Override tests use
	// t.Setenv.
	for _, k := range []string{agenthome.ClaudeEnv, agenthome.CodexEnv, agenthome.CursorEnv} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
