package tui

import (
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/i18n"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestSettingsLanguageRow_HotSwitch proves the language row is present,
// its set closure switches the live language immediately, and it writes
// config.Lang.
func TestSettingsLanguageRow_HotSwitch(t *testing.T) {
	i18n.SetLanguage("en")
	t.Cleanup(func() { i18n.SetLanguage("en") })

	st := styles.Default()
	m := newSettings(st, DefaultKeymap(), config.Config{}, "test")

	langField := findEditableField(t, m, "i18n.lang")
	if len(langField.options) != 2 || langField.options[0] != "en" || langField.options[1] != "zh" {
		t.Fatalf("language row options = %v, want [en zh]", langField.options)
	}

	cfg := config.Config{}
	if err := langField.set(&cfg, "zh"); err != nil {
		t.Fatalf("set zh: %v", err)
	}
	if i18n.Current() != i18n.LangZh {
		t.Errorf("after set zh, Current() = %q, want zh", i18n.Current())
	}
	if cfg.Lang != "zh" {
		t.Errorf("cfg.Lang = %q, want zh", cfg.Lang)
	}

	if err := langField.set(&cfg, "bogus"); err == nil {
		t.Errorf("set bogus: expected error")
	}
	if cfg.Lang != "zh" {
		t.Errorf("cfg.Lang changed after bogus set = %q, want zh unchanged", cfg.Lang)
	}
}

// findEditableField locates one row in the Settings list by its label
// (a TOML key such as "i18n.lang"), for tests that drive a specific row.
func findEditableField(t *testing.T, m settingsModel, label string) editableField {
	t.Helper()
	for _, f := range m.fields() {
		if f.label == label {
			return f
		}
	}
	t.Fatalf("no editableField with label %q; have %v", label, m.fields())
	return editableField{}
}
