// Package codexpath resolves the Codex data directory for configuration,
// transcript discovery, usage and deletion using the same environment.
package codexpath

import (
	"os"
	"path/filepath"
)

// Root honors CODEX_HOME, falling back to .codex under home.
func Root(home string) string {
	if root := os.Getenv("CODEX_HOME"); root != "" {
		return filepath.Clean(root)
	}
	return filepath.Join(home, ".codex")
}

// Sessions is the active rollout directory under Root.
func Sessions(home string) string { return filepath.Join(Root(home), "sessions") }
