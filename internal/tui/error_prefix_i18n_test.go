package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestSettings_ReadOnlyErrorTranslated — Enter on a read-only Settings
// row said "field is read-only: …" in English whatever the language.
func TestSettings_ReadOnlyErrorTranslated(t *testing.T) {
	withLang(t, "de")
	m := newSettings(styles.Default(), DefaultKeymap(), config.Defaults(), "test")
	fields := m.fields()
	idx := -1
	for i, f := range fields {
		if f.readOnly {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Skip("no read-only settings field")
	}
	m.cursor = idx
	m, _ = m.Update(keyMsg("enter"))
	if m.errMsg == "" {
		t.Fatal("enter on a read-only row showed no error")
	}
	if strings.Contains(m.errMsg, "field is read-only") {
		t.Errorf("read-only error is untranslated: %q", m.errMsg)
	}
	if !strings.HasPrefix(m.errMsg, "Feld ist schreibgeschützt: ") {
		t.Errorf("read-only error = %q, want the German prefix", m.errMsg)
	}
}

// TestClaudeModelPick_ErrorTranslated — a model pick that can't be
// saved reported "model not changed: …" in English whatever the
// language.
func TestClaudeModelPick_ErrorTranslated(t *testing.T) {
	fakeClaudeDir(t)
	withLang(t, "de")
	home, _ := os.UserHomeDir()
	cfgPath := filepath.Join(home, ".config", "ccmux", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("this is = = not toml ["), 0o600); err != nil {
		t.Fatal(err)
	}
	msg, ok := applyModelChoiceCmd(modelChoice{Settings: "sonnet"})().(claudeModelChangedMsg)
	if !ok || msg.Err == nil {
		t.Fatalf("a pick on a broken config.toml succeeded: %+v", msg)
	}
	if strings.Contains(msg.Err.Error(), "model not changed") {
		t.Errorf("model error is untranslated: %q", msg.Err)
	}
	if !strings.HasPrefix(msg.Err.Error(), "Modell nicht geändert: ") {
		t.Errorf("model error = %q, want the German prefix", msg.Err)
	}
}
