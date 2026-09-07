package tui

import (
	"os"
	"testing"

	"github.com/skzv/ccmux/internal/i18n"
)

// TestMain pins the package's global language to English for every test.
// Under en, tr() returns the key verbatim, so the existing golden/width
// snapshots (which assert English text) stay byte-identical. Tests that
// need Chinese opt in via withLang(t, "zh").
func TestMain(m *testing.M) {
	i18n.SetLanguage("en")
	os.Exit(m.Run())
}

// withLang sets the package language for the duration of one test and
// restores English afterwards, so a zh test can't leak into the next.
func withLang(t *testing.T, lang string) {
	t.Helper()
	i18n.SetLanguage(lang)
	t.Cleanup(func() { i18n.SetLanguage("en") })
}
