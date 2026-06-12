package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/claudemodels"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestCcmuxPicker_SetWritesConfigToml — the smoke test for the new
// picker. setCcmuxClaudeDefault must round-trip through
// config.Load/Save: write the user's pick into [claude] default_model
// without clobbering other config fields, and present it cleanly to
// the next config.Load. If a future refactor breaks the read-mutate-
// write idiom (e.g. someone uses os.WriteFile and forgets to read
// first), this catches the regression.
func TestCcmuxPicker_SetWritesConfigToml(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))

	// Seed a config with an existing, unrelated setting we want
	// preserved across the write.
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
		t.Errorf("DefaultModel = %q, want %q", got.Claude.DefaultModel, "claude-opus-4-8")
	}
	if got.Theme != "dracula" {
		t.Errorf("Theme should survive the write, got %q", got.Theme)
	}

	// Whitespace handling: a stray space in the picker model must
	// not leak into the TOML or the eventual ANTHROPIC_MODEL env var.
	if err := setCcmuxClaudeDefault("  haiku  "); err != nil {
		t.Fatalf("set with whitespace: %v", err)
	}
	got, _ = config.Load()
	if got.Claude.DefaultModel != "haiku" {
		t.Errorf("whitespace not trimmed: got %q", got.Claude.DefaultModel)
	}

	// Empty value clears the pin — the "(no pin)" sentinel row.
	if err := setCcmuxClaudeDefault(""); err != nil {
		t.Fatalf("set empty: %v", err)
	}
	got, _ = config.Load()
	if got.Claude.DefaultModel != "" {
		t.Errorf("empty pick should clear the pin: got %q", got.Claude.DefaultModel)
	}
}

// TestCcmuxPicker_FallbackOnly_RendersWithoutDaemon — the picker must
// render a usable list even when the daemon hasn't written its cache
// file yet (fresh install, daemon stopped, etc). The first row is
// always the "(no pin)" sentinel; the rest come from the curated
// fallback list.
func TestCcmuxPicker_FallbackOnly_RendersWithoutDaemon(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir) // sandboxes the cache file
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))

	m := newClaude(styles.Default(), DefaultKeymap())
	m.loadCatalog()
	rows := m.catalogPickerModels()
	if len(rows) < 2 {
		t.Fatalf("picker rows = %d, expected at least 1 sentinel + curated entries", len(rows))
	}
	if rows[0].ID != "" || !strings.Contains(rows[0].DisplayName, "no pin") {
		t.Errorf("first row should be the (no pin) sentinel, got %+v", rows[0])
	}
	// Spot-check that a curated entry made it through.
	foundOpus := false
	for _, r := range rows {
		if strings.HasPrefix(r.ID, "claude-opus-") {
			foundOpus = true
			break
		}
	}
	if !foundOpus {
		t.Errorf("picker should include curated opus rows: %+v", rows)
	}
}

// TestCcmuxPicker_LoadsDaemonCacheWhenPresent — when the daemon has
// written a catalog, the picker must surface it (not the curated
// fallback). Simulates the daemon's on-disk state by writing the
// cache file directly.
func TestCcmuxPicker_LoadsDaemonCacheWhenPresent(t *testing.T) {
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
	// Write a synthetic cache with a future-shipping model the
	// curated list won't have.
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
	rows := m.catalogPickerModels()
	foundFuture := false
	for _, r := range rows {
		if r.ID == "claude-opus-9-9" {
			foundFuture = true
			break
		}
	}
	if !foundFuture {
		t.Errorf("picker should surface the daemon-cached model: %+v", rows)
	}
}

// TestCcmuxModelLabel_TagsCurrent — the per-row label must call out
// the user's current pin so they can spot it without reading every
// row. Reads like a guardrail on a single helper, but the UX promise
// ("which one am I on?") is real.
func TestCcmuxModelLabel_TagsCurrent(t *testing.T) {
	mdl := claudemodels.Model{ID: "claude-opus-4-8", DisplayName: "Claude Opus 4.8"}
	if got := ccmuxModelLabel(mdl, "claude-opus-4-8"); !strings.Contains(got, "[current]") {
		t.Errorf("current pin should be tagged: %q", got)
	}
	if got := ccmuxModelLabel(mdl, "claude-haiku-4-5"); strings.Contains(got, "[current]") {
		t.Errorf("non-current row should not be tagged: %q", got)
	}
}
