package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/i18n"
)

// helpFooterLine is the help modal's footer as drawn: the last text
// row inside its border.
func helpFooterLine(t *testing.T, frame string) string {
	t.Helper()
	lines := strings.Split(ansi.Strip(frame), "\n")
	for i := len(lines) - 1; i > 0; i-- {
		if strings.Contains(lines[i], "╰") {
			return strings.Trim(lines[i-1], "│ ")
		}
	}
	t.Fatalf("no help modal in frame:\n%s", ansi.Strip(frame))
	return ""
}

// TestHelpFooter_CloseHintSurvivesNarrowWidths — at 40 columns, once
// the scroll counter had two digits the footer was cut off mid-word
// ("↑↓ scroll 21/21 · ? or esc to clo…"), and some translations of
// the plain close hint didn't fit either. A shorter form is used when
// the full one doesn't fit, so the footer always ends whole and still
// says how to close the help.
func TestHelpFooter_CloseHintSurvivesNarrowWidths(t *testing.T) {
	withLang(t, "en")
	for _, code := range i18n.Codes() {
		for _, size := range [][2]int{{40, 12}, {40, 60}, {32, 12}, {80, 24}} {
			t.Run(fmt.Sprintf("%s/%dx%d", code, size[0], size[1]), func(t *testing.T) {
				i18n.SetLanguage(code)
				defer i18n.SetLanguage("en")
				a := newHomeApp(t, size[0], size[1], 3)
				a, _ = updateApp(t, a, keyMsg("?"))
				a, _ = updateApp(t, a, keyMsg("end"))
				footer := helpFooterLine(t, a.View())
				if strings.HasSuffix(footer, "…") {
					t.Errorf("help footer cut off: %q", footer)
				}
				closeWord := "esc"
				if code == "fr" {
					closeWord = "échap"
				}
				if !strings.Contains(strings.ToLower(footer), closeWord) {
					t.Errorf("help footer %q doesn't say how to close", footer)
				}
				if max := a.helpScrollMax(); max > 0 && !strings.Contains(footer, fmt.Sprintf("%d/%d", max+1, max+1)) {
					t.Errorf("help footer %q lost the scroll position %d/%d", footer, max+1, max+1)
				}
			})
		}
	}
}
