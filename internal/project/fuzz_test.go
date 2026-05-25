package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

// FuzzReadAgent exercises the per-project agent sidecar reader with
// arbitrary file contents. The contract:
//
//  1. ReadAgent never panics — a user hand-editing `.ccmux/agent` to
//     anything must not crash the daemon's poll loop.
//  2. The returned id is always one of the canonical agent IDs (claude /
//     codex / antigravity / cursor). Unrecognized inputs fall back to claude
//     per the back-compat spec. The legacy "gemini" body is allowed
//     via ParseID's back-compat alias and resolves to antigravity.
//
// We materialize each fuzz input as a real sidecar file under a
// fresh /tmp dir per invocation, then read it back. This is the
// integration path — same syscalls the daemon takes — so the
// fuzzer catches any new file-handling bug at the same layer where
// it would surface in production.
func FuzzReadAgent(f *testing.F) {
	// Seed with the shapes the unit tests already cover, plus
	// extreme cases for the fuzzer to mutate from.
	for _, seed := range []string{
		"claude",
		"codex\n",
		"  antigravity  ",
		"ANTIGRAVITY\n\n",
		"gemini", // back-compat alias
		"GEMINI\n\n",
		"cursor",
		"",
		"claude-3-sonnet", // close-to-valid garbage
		"\x00\x00\x00",
		"a very long string that goes on and on and on and on…",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		dir := t.TempDir()
		// Write the sidecar — recreating the layout SetAgent would
		// produce, but with arbitrary content so we exercise the
		// read path's tolerance.
		if err := os.MkdirAll(filepath.Join(dir, ".ccmux"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".ccmux", "agent"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		got := ReadAgent(dir)
		switch got {
		case agent.IDClaude, agent.IDCodex, agent.IDAntigravity, agent.IDCursor:
			// canonical — good
		default:
			t.Fatalf("ReadAgent(body=%q) = %q — must be one of {claude,codex,antigravity,cursor}", body, got)
		}
	})
}
