package cursorconfig

import (
	"path/filepath"
	"testing"
)

// TestPaths_CursorHome — Paths reads $CURSOR_HOME through the same
// resolver as the Cursor transcript listing and the ai-tracking
// reader, so the Cursor tab and the conversation list agree: default
// ~/.cursor, the variable when set, ~ expanded (it used to become a
// relative directory named "~").
func TestPaths_CursorHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := []struct{ value, want string }{
		{"", filepath.Join(home, ".cursor")},
		{"~/cursor-work", filepath.Join(home, "cursor-work")},
	}
	for _, tc := range cases {
		t.Setenv("CURSOR_HOME", tc.value)
		got, err := Paths()
		if err != nil {
			t.Fatal(err)
		}
		if got.Root != tc.want || got.Hooks != filepath.Join(tc.want, "hooks.json") {
			t.Errorf("CURSOR_HOME=%q: Paths = %+v, want root %s", tc.value, got, tc.want)
		}
	}
}
