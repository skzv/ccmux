package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/claudeconfig"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// modelPickEnv isolates HOME (config.toml) and the Claude config dir.
func modelPickEnv(t *testing.T, settingsJSON string) (home, claudeDir string) {
	t.Helper()
	home = t.TempDir()
	claudeDir = filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(settingsJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, claudeDir
}

func claudeSettingsModel(t *testing.T) string {
	t.Helper()
	s, err := claudeconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	return s.Model
}

// TestModelPick_BrokenConfigChangesNothing — with a config.toml that
// doesn't parse, the pick used to write settings.json and only then fail
// on "parse config": half applied, while the Agents tab kept showing the
// old model. Now nothing is written and the error says so.
func TestModelPick_BrokenConfigChangesNothing(t *testing.T) {
	home, _ := modelPickEnv(t, `{"model": "sonnet"}`)
	cfgPath := filepath.Join(home, ".config", "ccmux", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("theme = [unterminated\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := applyModelChoiceCmd(modelChoice{Settings: "claude-opus-4-8", Pin: "claude-opus-4-8"})().(claudeModelChangedMsg)
	if msg.Err == nil {
		t.Fatal("a pick with a broken config.toml reported success")
	}
	if got := claudeSettingsModel(t); got != "sonnet" {
		t.Errorf("settings.json model = %q after a failed pick, want it untouched (sonnet)", got)
	}
	m := newClaude(styles.Default(), DefaultKeymap())
	_, cmd := m.Update(msg)
	toast, ok := cmd().(toastMsg)
	if !ok || toast.Kind != toastError || !strings.Contains(toast.Text, "not changed") {
		t.Errorf("failed pick toast = %+v, want an error saying the model was not changed", toast)
	}
}

// TestModelPick_SettingsWriteFailureRestoresPin — when settings.json
// can't be written after the pin was saved, the pin goes back to what it
// was, so the two never disagree.
func TestModelPick_SettingsWriteFailureRestoresPin(t *testing.T) {
	modelPickEnv(t, `{"model": "sonnet"}`)
	if _, err := setCcmuxClaudeDefault("claude-haiku-4-5"); err != nil {
		t.Fatal(err)
	}
	// settings.json is a directory → claudeconfig.SetModel fails.
	blocker := filepath.Join(t.TempDir(), "claude")
	if err := os.MkdirAll(filepath.Join(blocker, "settings.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", blocker)

	msg := applyModelChoiceCmd(modelChoice{Settings: "claude-opus-4-8", Pin: "claude-opus-4-8"})().(claudeModelChangedMsg)
	if msg.Err == nil {
		t.Fatal("a pick whose settings.json write failed reported success")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Claude.DefaultModel != "claude-haiku-4-5" {
		t.Errorf("ccmux pin = %q after the settings.json write failed, want it restored to claude-haiku-4-5", cfg.Claude.DefaultModel)
	}
}

// TestModelPick_AppliesBoth — the happy path still writes both.
func TestModelPick_AppliesBoth(t *testing.T) {
	modelPickEnv(t, `{"model": "sonnet"}`)
	msg := applyModelChoiceCmd(modelChoice{Settings: "claude-opus-4-8", Pin: "claude-opus-4-8"})().(claudeModelChangedMsg)
	if msg.Err != nil {
		t.Fatal(msg.Err)
	}
	if got := claudeSettingsModel(t); got != "claude-opus-4-8" {
		t.Errorf("settings.json model = %q", got)
	}
	if msg.Cfg == nil || msg.Cfg.Claude.DefaultModel != "claude-opus-4-8" {
		t.Errorf("saved pin = %+v", msg.Cfg)
	}
}
