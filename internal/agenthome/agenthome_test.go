package agenthome

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestOverride pins how a variable's value becomes a directory: unset
// or blank means "use the default", a tilde expands against the home
// the caller passed, the result is cleaned, and a value that is still
// relative is ignored rather than resolved against ccmux's cwd.
func TestOverride(t *testing.T) {
	home := filepath.FromSlash("/home/me")
	abs := filepath.FromSlash("/srv/claude-work")
	if runtime.GOOS == "windows" {
		home, abs = `C:\Users\me`, `D:\claude-work`
	}
	sep := string(filepath.Separator)
	cases := []struct {
		name, value string
		home        string
		want        string
		ok          bool
	}{
		{name: "unset", value: "", home: home},
		{name: "blank", value: "   ", home: home},
		{name: "absolute", value: abs, home: home, want: abs, ok: true},
		{name: "trailing slash", value: abs + sep, home: home, want: abs, ok: true},
		{name: "dot segments", value: abs + sep + "." + sep + "x" + sep + "..", home: home, want: abs, ok: true},
		{name: "tilde alone", value: "~", home: home, want: home, ok: true},
		{name: "tilde slash", value: "~/.claude-work", home: home, want: filepath.Join(home, ".claude-work"), ok: true},
		{name: "tilde trailing slash", value: "~/.claude-work/", home: home, want: filepath.Join(home, ".claude-work"), ok: true},
		{name: "tilde without home", value: "~/.claude-work", home: ""},
		{name: "tilde user", value: "~other/.claude", home: home},
		{name: "relative", value: ".claude-work", home: home},
		{name: "relative dot", value: "./claude-work", home: home},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(ClaudeEnv, tc.value)
			got, ok := Override(ClaudeEnv, tc.home)
			if got != tc.want || ok != tc.ok {
				t.Errorf("Override(%q) = %q, %v; want %q, %v", tc.value, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestRoots checks every agent's resolver against its own variable:
// unset gives the historical default under home, set gives the
// variable's directory, and an unusable value falls back to the
// default instead of a relative path.
func TestRoots(t *testing.T) {
	home := t.TempDir()
	custom := t.TempDir()
	cases := []struct {
		env        string
		root, sub  func(string) string
		def, child string
	}{
		{ClaudeEnv, Claude, ClaudeProjects, ".claude", "projects"},
		{CodexEnv, Codex, CodexSessions, ".codex", "sessions"},
		{CursorEnv, Cursor, CursorProjects, ".cursor", "projects"},
	}
	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			for _, other := range []string{ClaudeEnv, CodexEnv, CursorEnv} {
				t.Setenv(other, "")
			}
			def := filepath.Join(home, tc.def)
			check := func(label, wantRoot string) {
				t.Helper()
				if got := tc.root(home); got != wantRoot {
					t.Errorf("%s: root = %s, want %s", label, got, wantRoot)
				}
				if got := tc.sub(home); got != filepath.Join(wantRoot, tc.child) {
					t.Errorf("%s: transcripts = %s, want %s", label, got, filepath.Join(wantRoot, tc.child))
				}
			}
			check("unset", def)
			t.Setenv(tc.env, custom)
			check("set", custom)
			t.Setenv(tc.env, custom+string(filepath.Separator))
			check("trailing slash", custom)
			t.Setenv(tc.env, "~/relocated")
			check("tilde", filepath.Join(home, "relocated"))
			t.Setenv(tc.env, "relocated")
			check("relative", def)
		})
	}
}

// TestRoots_Independent — one agent's variable must not move another's.
func TestRoots_Independent(t *testing.T) {
	home := t.TempDir()
	t.Setenv(ClaudeEnv, t.TempDir())
	t.Setenv(CodexEnv, "")
	t.Setenv(CursorEnv, "")
	if got := Codex(home); got != filepath.Join(home, ".codex") {
		t.Errorf("CLAUDE_CONFIG_DIR moved Codex to %s", got)
	}
	if got := Cursor(home); got != filepath.Join(home, ".cursor") {
		t.Errorf("CLAUDE_CONFIG_DIR moved Cursor to %s", got)
	}
}
