package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// TestSettingsSave_KeepsConfigComments — changing a row on the Settings
// screen used to re-encode config.toml and drop every comment the user
// had written in it. Now only the changed value moves.
func TestSettingsSave_KeepsConfigComments(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := config.Save(config.Defaults()); err != nil {
		t.Fatal(err)
	}
	p, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	base, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	annotated := "# laptop config, see the dotfiles repo\n\n" +
		strings.Replace(string(base), "[agents]\n", "# which agent new projects get\n[agents]\n", 1)
	if err := os.WriteFile(p, []byte(annotated), 0o600); err != nil {
		t.Fatal(err)
	}

	app := New(config.Defaults(), "test")
	app.tour.Close()
	app = cycleDefaultAgent(t, app)
	next := app.settings.cfg.Agents.Default
	if next == "claude" {
		t.Fatal("cycling agents.default did not change it")
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(annotated, `default = "claude"`, `default = "`+next+`"`, 1)
	if string(got) != want {
		t.Errorf("Settings save rewrote more than agents.default:\n--- got\n%s\n--- want\n%s", got, want)
	}
}
