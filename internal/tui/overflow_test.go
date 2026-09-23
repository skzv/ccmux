package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const longToast = "tmux: attach-session failed: can't find session: c-some-really-long-project-name-that-keeps-going (exit status 1)"

// TestOverlays_NeverWiderThanTerminal — boxes wider than the terminal
// get hard-truncated by Bubble Tea's renderer (the right border and the
// end of the text vanish). The error toast (unbounded — ~108 cols for a
// 100-char message at 50 cols), the tour card (50-col floor) and the
// attach overlay (no cap) all overflowed a phone-width terminal. Every
// line of the frame must fit, and the box must stay intact.
func TestOverlays_NeverWiderThanTerminal(t *testing.T) {
	cases := []struct {
		name   string
		widths []int
		mut    func(a *App)
		anchor string // text that must still be visible
	}{
		{"error toast", []int{40, 50, 80}, func(a *App) {
			a.toasts.Set(toastError, longToast, 10*time.Second)
		}, "tmux: attach-session"},
		{"first-run tour", []int{30, 45, 55}, func(a *App) { a.tour.Open() }, ""},
		{"attach overlay", []int{24, 40}, func(a *App) {
			a.startAttaching(attachKindOpening, "some-rather-long-project-name")
		}, "Opening"},
	}
	for _, tc := range cases {
		for _, w := range tc.widths {
			t.Run(fmt.Sprintf("%s/%d", tc.name, w), func(t *testing.T) {
				a := newHomeApp(t, w, 30, 3)
				tc.mut(&a)
				out := a.View()
				assertNoOverflow(t, out, w)
				if h := lipgloss.Height(out); h > 30 {
					t.Errorf("frame is %d lines tall at 30 rows", h)
				}
				if tc.anchor != "" && !strings.Contains(out, tc.anchor) {
					t.Errorf("%q missing from the frame:\n%s", tc.anchor, out)
				}
				// The box must be closed on both sides: every line that
				// opens a rounded top border also closes it.
				for _, line := range strings.Split(out, "\n") {
					if strings.Contains(line, "╭") && !strings.Contains(line, "╮") {
						t.Errorf("box top border cut off: %q", line)
					}
				}
			})
		}
	}
}

// TestToastRender_WrapsWithinWidth pins the bubble contract: never wider
// than maxWidth, wraps a long message onto at most toastMaxLines lines
// (ellipsis when clipped), and leaves a short message on one line.
func TestToastRender_WrapsWithinWidth(t *testing.T) {
	st := newHomeApp(t, 80, 24, 0).styles
	var c toastController
	c.Set(toastError, longToast, 10*time.Second)
	for _, w := range []int{20, 48, 60} {
		out := c.Render(st, w)
		assertNoOverflow(t, out, w)
		if got := lipgloss.Height(out); got > toastMaxLines+2 {
			t.Errorf("w=%d: toast is %d lines, want at most %d text lines plus border", w, got, toastMaxLines)
		}
	}
	if out := c.Render(st, 20); !strings.Contains(out, "…") {
		t.Errorf("clipped toast should end with an ellipsis:\n%s", out)
	}
	c.Set(toastInfo, "saved", 3*time.Second)
	if got := lipgloss.Height(c.Render(st, 60)); got != 3 {
		t.Errorf("short toast should stay one line (3 with border), got %d", got)
	}
	if got, want := c.Render(st, 0), c.Render(st, 200); got != want {
		t.Errorf("maxWidth<=0 should be unbounded:\n%q\n%q", got, want)
	}
}
