package tui

import (
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// cycleDefaultAgent presses Enter on the Settings agents.default row and
// feeds the resulting command's message back through the App, the way
// the Bubble Tea runtime would.
func cycleDefaultAgent(t *testing.T, app App) App {
	t.Helper()
	for i, f := range app.settings.fields() {
		if f.label == "agents.default" {
			app.settings.cursor = i
		}
	}
	var cmd tea.Cmd
	app.settings, cmd = app.settings.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("saving a Settings row should emit configSavedMsg")
	}
	model, _ := app.Update(cmd())
	return model.(App)
}

// TestSettingsSave_ReachesAppAndSurvivesTour — a Settings edit must
// become the App's config (so new sessions use it) and must not be
// reverted when the tour later records itself as shown.
func TestSettingsSave_ReachesAppAndSurvivesTour(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := config.Save(config.Defaults()); err != nil {
		t.Fatal(err)
	}
	app := New(config.Defaults(), "test")
	app.tour.Close()

	app = cycleDefaultAgent(t, app)
	want := app.settings.cfg.Agents.Default
	if want == "claude" {
		t.Fatal("cycling agents.default did not change it")
	}
	if app.cfg.Agents.Default != want {
		t.Fatalf("App.cfg.Agents.Default = %q, want %q after the Settings save", app.cfg.Agents.Default, want)
	}

	app.markTourShown()
	disk, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if disk.Agents.Default != want {
		t.Errorf("tour save reverted the Settings edit: agents.default = %q", disk.Agents.Default)
	}
	if !disk.Tour.Shown {
		t.Error("tour not recorded as shown")
	}
}

// TestDetectedTierAndRuntimeOverrides_NeverPersisted — the
// auto-detected Claude tier and per-run flags (--projects) are shown in
// the UI but must not be written to config.toml by unrelated saves.
func TestDetectedTierAndRuntimeOverrides_NeverPersisted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := config.Save(config.Defaults()); err != nil {
		t.Fatal(err)
	}
	onDiskRoot := config.Defaults().Projects.Root
	overrides := func(c *config.Config) { c.Projects.Root = "/tmp/override-root" }
	cfg := config.Defaults()
	overrides(&cfg)
	app := New(cfg, "test")
	app.SetRuntimeOverrides(overrides)
	app.tour.Close()

	model, _ := app.Update(tierDetectedMsg{Tier: "max20x"})
	app = model.(App)
	if got := app.cfg.Subscription.TierFor("claude"); got != "max20x" {
		t.Fatalf("detected tier not shown: %q", got)
	}

	app = cycleDefaultAgent(t, app)
	app.markTourShown()

	if got := app.cfg.Subscription.TierFor("claude"); got != "max20x" {
		t.Errorf("detected tier lost after adopting saved config: %q", got)
	}
	if app.cfg.Projects.Root != "/tmp/override-root" {
		t.Errorf("runtime override lost after adopting saved config: %q", app.cfg.Projects.Root)
	}
	disk, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := disk.Subscription.TierFor("claude"); got == "max20x" {
		t.Error("auto-detected tier was written to config.toml")
	}
	if disk.Projects.Root != onDiskRoot {
		t.Errorf("--projects override was written to config.toml: %q", disk.Projects.Root)
	}
}

// TestSettingsSave_RefusedWhenConfigUnparseable — with a broken
// config.toml the TUI runs on defaults; saving a row must not replace
// the user's file with those defaults.
func TestSettingsSave_RefusedWhenConfigUnparseable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Save(config.Defaults()); err != nil {
		t.Fatal(err)
	}
	broken := "[[hosts]]\nname = \"mini\"\naddress =\n"
	if err := os.WriteFile(p, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newSettings(styles.Default(), DefaultKeymap(), config.Defaults(), "test")
	for i, f := range editableFields() {
		if f.label == "agents.default" {
			m.cursor = i
		}
	}
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("a refused save must not emit configSavedMsg")
	}
	if m.errMsg == "" {
		t.Error("expected an inline save error")
	}
	if got, _ := os.ReadFile(p); string(got) != broken {
		t.Errorf("unparseable config.toml was overwritten:\n%s", got)
	}
}
