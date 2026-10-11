package codexpath

import (
	"path/filepath"
	"testing"
)

func TestRoot(t *testing.T) {
	home := t.TempDir()
	for _, override := range []string{"", t.TempDir()} {
		t.Run(override, func(t *testing.T) {
			t.Setenv("CODEX_HOME", override)
			want := override
			if want == "" {
				want = filepath.Join(home, ".codex")
			}
			if got := Root(home); got != want {
				t.Errorf("root = %s, want %s", got, want)
			}
			if got := Sessions(home); got != filepath.Join(want, "sessions") {
				t.Errorf("sessions = %s", got)
			}
		})
	}
}
