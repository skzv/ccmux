package agent

import (
	"path/filepath"
	"testing"
)

// TestRoots_FollowAgentHomeOverrides — ConfigRoot and TranscriptsRoot
// are what the usage walkers and the Agents tab read. For Claude and
// Cursor they hard-coded ~/.claude and ~/.cursor, so with
// CLAUDE_CONFIG_DIR or CURSOR_HOME set they pointed at a directory the
// agent never wrote to. They now follow the same resolver as the
// config readers (internal/agenthome), Codex included.
func TestRoots_FollowAgentHomeOverrides(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		a     Agent
		env   string
		trans string
	}{
		{Claude{}, "CLAUDE_CONFIG_DIR", "projects"},
		{Cursor{}, "CURSOR_HOME", "projects"},
		{Codex{}, "CODEX_HOME", "sessions"},
	}
	for _, tc := range cases {
		t.Run(string(tc.a.ID()), func(t *testing.T) {
			custom := t.TempDir()
			t.Setenv(tc.env, custom)
			if got := tc.a.ConfigRoot(home); got != custom {
				t.Errorf("ConfigRoot = %s, want %s", got, custom)
			}
			if got := tc.a.TranscriptsRoot(home); got != filepath.Join(custom, tc.trans) {
				t.Errorf("TranscriptsRoot = %s, want %s", got, filepath.Join(custom, tc.trans))
			}
			t.Setenv(tc.env, "~/moved")
			if got := tc.a.ConfigRoot(home); got != filepath.Join(home, "moved") {
				t.Errorf("ConfigRoot with ~/moved = %s, want %s", got, filepath.Join(home, "moved"))
			}
		})
	}
}
