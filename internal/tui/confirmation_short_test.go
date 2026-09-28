package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/daemon"
)

// visibleScreen is what Bubble Tea actually draws for a frame on a
// height-row terminal: the standard renderer keeps only the last
// height lines of a taller frame.
func visibleScreen(frame string, height int) string {
	lines := strings.Split(frame, "\n")
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	return strings.Join(lines, "\n")
}

// screenButton finds a translated dialog button on a drawn screen: the
// row holding both button labels and the column of label's middle.
func screenButton(t *testing.T, screen, label string) (x, y int) {
	t.Helper()
	text := " " + label + " "
	for row, line := range strings.Split(ansi.Strip(screen), "\n") {
		if i := strings.LastIndex(line, text); i >= 0 && strings.Contains(line, " "+tr("Cancel")+" ") {
			return ansi.StringWidth(line[:i]) + ansi.StringWidth(text)/2, row
		}
	}
	t.Fatalf("button %q not found on screen:\n%s", label, ansi.Strip(screen))
	return 0, 0
}

// TestConfirmation_ShortTerminalClicks — a dialog taller than the
// terminal (the kill dialog at 40x12) used to be drawn clipped while its
// clicks were matched against the unclipped dialog: every row was
// shifted, so clicking the visible "Kill" did nothing and clicking the
// blank row under it killed the session. The dialog now fits the
// screen, keeps its buttons on it, and clicks land where they are drawn.
func TestConfirmation_ShortTerminalClicks(t *testing.T) {
	stubCurrentTmuxSession(t, "")
	for _, lang := range []string{"en", "de", "ru"} {
		for _, size := range [][2]int{{40, 12}, {40, 9}, {60, 8}, {40, 6}} {
			for _, kind := range []string{"kill", "quit"} {
				t.Run(fmt.Sprintf("%s/%s/%dx%d", lang, kind, size[0], size[1]), func(t *testing.T) {
					withLang(t, lang)
					a := newConfirmationTestApp()
					a.width, a.height = size[0], size[1]
					a.sessionsM.SetSessions([]daemon.SessionState{{Name: "c-project-name", Host: "local"}})
					key, confirmLabel := "x", "Kill"
					if kind == "quit" {
						key, confirmLabel = "q", "Quit"
					}
					a, _ = sendKey(t, a, keyRunes(key))
					if !a.confirm.open() {
						t.Fatalf("%s did not open the dialog", key)
					}
					frame := a.View()
					if got := len(strings.Split(frame, "\n")); got > a.height {
						t.Errorf("dialog frame is %d lines on a %d-line terminal", got, a.height)
					}
					screen := visibleScreen(frame, a.height)
					assertNoOverflow(t, screen, a.width)

					cx, cy := screenButton(t, screen, tr("Cancel"))
					// The row under the buttons is not a button.
					if cy+1 < a.height {
						if a2, cmd := click(t, a, cx, cy+1); !a2.confirm.open() || cmd != nil {
							t.Fatal("a click below the buttons acted on the dialog")
						}
					}
					if a2, cmd := click(t, a, cx, cy); a2.confirm.open() || cmd != nil {
						t.Fatalf("clicking the visible Cancel did not just close the dialog (open=%v)", a2.confirm.open())
					}

					x, y := screenButton(t, screen, tr(confirmLabel))
					a3, cmd := click(t, a, x, y)
					if a3.confirm.open() || cmd == nil {
						t.Fatalf("clicking the visible %s did nothing", confirmLabel)
					}
					if kind == "quit" && !commandContainsQuit(cmd) {
						t.Fatal("clicking Quit did not quit")
					}
				})
			}
		}
	}
}
