package conversations

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Default-root fixtures must never discover or delete real rollouts
	// through an inherited override. Override tests use t.Setenv.
	_ = os.Unsetenv("CODEX_HOME")
	os.Exit(m.Run())
}
