package tui

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/i18n"
	"github.com/skzv/ccmux/internal/tui/styles"
)

func TestTour_ReopenAfterLanguageSwitch(t *testing.T) {
	withLang(t, "en")
	tour := newTour(styles.Default())
	for _, lang := range []string{"zh", "en"} {
		i18n.SetLanguage(lang)
		tour.Open()
		if !strings.Contains(tour.View(100, 40), i18n.T("Welcome to ccmux")) {
			t.Fatalf("tour did not use %s after reopening", lang)
		}
		if tour.Step() != 0 || !tour.Next() {
			t.Fatal("reopened tour must start at step zero and support navigation")
		}
		tour.Close()
	}
}
