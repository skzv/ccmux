package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skzv/ccmux/internal/claudemodels"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestModelPin_SetWritesConfigToml — setCcmuxClaudeDefault (the pin
// write the unified picker performs alongside the settings.json write)
// must round-trip through config.Load/Save: write the user's pick into
// [claude] default_model without clobbering other config fields. If a
// future refactor breaks the read-mutate-write idiom (e.g. os.WriteFile
// without reading first), this catches the regression.
func TestModelPin_SetWritesConfigToml(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))

	// Seed a config with an unrelated setting we want preserved.
	seed := config.Defaults()
	seed.Theme = "dracula"
	if err := config.Save(seed); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	if err := setCcmuxClaudeDefault("claude-opus-4-8"); err != nil {
		t.Fatalf("setCcmuxClaudeDefault: %v", err)
	}
	got, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Claude.DefaultModel != "claude-opus-4-8" {
		t.Errorf("DefaultModel = %q, want claude-opus-4-8", got.Claude.DefaultModel)
	}
	if got.Theme != "dracula" {
		t.Errorf("Theme should survive the write, got %q", got.Theme)
	}

	// Whitespace must be trimmed so it can't leak into ANTHROPIC_MODEL.
	if err := setCcmuxClaudeDefault("  haiku  "); err != nil {
		t.Fatalf("set with whitespace: %v", err)
	}
	got, _ = config.Load()
	if got.Claude.DefaultModel != "haiku" {
		t.Errorf("whitespace not trimmed: got %q", got.Claude.DefaultModel)
	}

	// Empty clears the pin.
	if err := setCcmuxClaudeDefault(""); err != nil {
		t.Fatalf("set empty: %v", err)
	}
	got, _ = config.Load()
	if got.Claude.DefaultModel != "" {
		t.Errorf("empty pick should clear the pin: got %q", got.Claude.DefaultModel)
	}
}

// hasChoiceSetting reports whether any unified-picker row sets the given
// value (alias or full ID).
func hasChoiceSetting(m claudeModel, settings string) bool {
	for _, c := range m.unifiedModelChoices() {
		if c.Settings == settings {
			return true
		}
	}
	return false
}

// TestModelPicker_FallbackOnly_RendersWithoutDaemon — the picker must
// list usable models even when the daemon hasn't written its cache yet
// (fresh install, daemon stopped). The curated fallback carries the
// current model IDs.
func TestModelPicker_FallbackOnly_RendersWithoutDaemon(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))

	m := newClaude(styles.Default(), DefaultKeymap())
	m.loadCatalog()
	choices := m.unifiedModelChoices()
	if len(choices) < 2 {
		t.Fatalf("choices = %d, want the inherit sentinel + curated models + aliases", len(choices))
	}
	if choices[0].Settings != "" {
		t.Errorf("first row should be the inherit/clear sentinel, got %+v", choices[0])
	}
	if !hasChoiceSetting(m, "claude-opus-4-8") {
		t.Errorf("picker should include the curated current model claude-opus-4-8")
	}
	// Aliases are present too (the "always latest" option).
	if !hasChoiceSetting(m, "opus") {
		t.Errorf("picker should include the opus alias row")
	}
}

// TestModelPicker_LoadsDaemonCacheWhenPresent — when the daemon has
// written a catalog, the picker surfaces it (not just the curated
// fallback).
func TestModelPicker_LoadsDaemonCacheWhenPresent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))

	cachePath, err := claudemodels.CachePath()
	if err != nil {
		t.Fatalf("CachePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	cat := claudemodels.Catalog{
		Models: []claudemodels.Model{{
			ID:          "claude-opus-9-9",
			DisplayName: "Future Opus",
			Family:      "opus",
			Source:      claudemodels.SourceAPI,
		}},
		Source: claudemodels.SourceAPI,
	}
	if err := (claudemodels.Cache{Path: cachePath}).Write(cat); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	m := newClaude(styles.Default(), DefaultKeymap())
	m.loadCatalog()
	if !hasChoiceSetting(m, "claude-opus-9-9") {
		t.Errorf("picker should surface the daemon-cached model claude-opus-9-9")
	}
}
