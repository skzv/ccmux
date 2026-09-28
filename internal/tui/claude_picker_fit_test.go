package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/i18n"
)

// TestClaudePicker_FitsShortTerminals — the model picker (Agents → m)
// budgeted its list as if the subtitle were one line; it wraps on a
// narrow terminal, so the list ran long and the App's body clamp cut
// off the bottom border (60x16) or the hint and the border (40x20).
// The whole dialog, border and hint included, must stay on screen.
func TestClaudePicker_FitsShortTerminals(t *testing.T) {
	fakeClaudeDir(t)
	withLang(t, "en")
	for _, key := range []string{"m", "e"} {
		for _, code := range []string{"en", "de", "ja"} {
			for _, size := range [][2]int{{60, 16}, {40, 20}, {80, 24}, {50, 18}, {40, 14}, {60, 12}, {100, 12}} {
				t.Run(fmt.Sprintf("%s/%s/%dx%d", key, code, size[0], size[1]), func(t *testing.T) {
					i18n.SetLanguage(code)
					defer i18n.SetLanguage("en")
					a := newAppForTest(t)
					a.width, a.height = size[0], size[1]
					a.screen = ScreenAgents
					a, _ = updateApp(t, a, keyMsg(key))
					if !a.agentsM.claude.PickerOpen() {
						t.Fatalf("%s did not open its picker", key)
					}
					frame := ansi.Strip(a.View())
					if n := len(strings.Split(frame, "\n")); n > a.height {
						t.Errorf("frame is %d lines on a %d-line terminal", n, a.height)
					}
					if !strings.Contains(frame, "╰") {
						t.Errorf("picker lost its bottom border:\n%s", frame)
					}
					hint := strings.Fields(tr("↑↓ navigate  enter: choose  esc: cancel"))
					if !strings.Contains(frame, hint[len(hint)-1]) {
						t.Errorf("picker lost its key hint:\n%s", frame)
					}
				})
			}
		}
	}
}
