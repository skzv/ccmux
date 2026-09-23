package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
)

// cycleClaudeTier presses Enter on the Settings claude.tier row and
// feeds the resulting save back through the App, as the runtime does.
func cycleClaudeTier(t *testing.T, app App) App {
	t.Helper()
	for i, f := range app.settings.fields() {
		if f.label == "claude.tier" {
			app.settings.cursor = i
		}
	}
	var cmd tea.Cmd
	app.settings, cmd = app.settings.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("cycling claude.tier should emit configSavedMsg")
	}
	model, _ := app.Update(cmd())
	return model.(App)
}

// TestSettings_ClaudeTierCycleWithDetectedPlan — regression: once a
// paid plan was auto-detected, cycling claude.tier could never reach
// api or pro. Saving "api" was treated as "unset", so the App overlaid
// the detected plan straight back and the next Enter cycled from it
// again (max5x → max20x → "api"(shown as max5x) → max20x …). With a
// detected plan the cycle must visit all four tiers, and whatever is
// saved must be what the row then shows.
func TestSettings_ClaudeTierCycleWithDetectedPlan(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := config.Save(config.Defaults()); err != nil {
		t.Fatal(err)
	}
	app := New(config.Defaults(), "test")
	app.tour.Close()
	model, _ := app.Update(tierDetectedMsg{Tier: "max5x"})
	app = model.(App)
	if got := app.settings.cfg.Subscription.TierFor("claude"); got != "max5x" {
		t.Fatalf("setup: detected plan not shown, tier = %q", got)
	}

	seen := map[string]bool{}
	for _, want := range []string{"max20x", "api", "pro", "max5x", "max20x"} {
		app = cycleClaudeTier(t, app)
		disk, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		if got := disk.Subscription.TierFor("claude"); got != want {
			t.Fatalf("saved tier = %q, want %q", got, want)
		}
		if got := app.settings.cfg.Subscription.TierFor("claude"); got != want {
			t.Fatalf("after saving %q the row shows %q (detected plan overlaid over the user's choice)", want, got)
		}
		if got := app.cfg.Subscription.TierFor("claude"); got != want {
			t.Fatalf("after saving %q the App uses tier %q", want, got)
		}
		seen[want] = true
	}
	for _, tier := range []string{"api", "pro", "max5x", "max20x"} {
		if !seen[tier] {
			t.Errorf("cycle never reached %q", tier)
		}
	}
}

// TestTierDetectedMsg_ExplicitAPIWins — an explicit "api" in
// config.toml is the user's choice; a detected paid plan must not
// replace it.
func TestTierDetectedMsg_ExplicitAPIWins(t *testing.T) {
	cfg := config.Config{}
	cfg.Subscription.Tier = "api"
	a := New(cfg, "test")
	next, _ := a.Update(tierDetectedMsg{Tier: "max20x"})
	if got := next.(App).cfg.Subscription.Tier; got != "api" {
		t.Errorf("tier = %q, want the explicit api kept", got)
	}
}

// TestConfigDefaults_ClaudeTierUnset — the default must be unset, not
// "api": every config save used to write the default `tier = "api"`,
// which (now that api is an explicit choice) would disable the
// detected-plan display for users who never picked a tier.
func TestConfigDefaults_ClaudeTierUnset(t *testing.T) {
	if got := config.Defaults().Subscription.TierFor("claude"); got != "" {
		t.Errorf("Defaults claude tier = %q, want unset", got)
	}
}
