package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// assertFrameFits fails when the frame is taller or wider than the
// terminal, or when a box in it lost its bottom border (content taller
// than a pane used to push the border off the frame).
func assertFrameFits(t *testing.T, out string, w, h int) {
	t.Helper()
	assertNoOverflow(t, out, w)
	if got := lipgloss.Height(out); got > h {
		t.Errorf("frame is %d rows on a %d-row terminal", got, h)
	}
	plain := ansi.Strip(out)
	if tops, bottoms := strings.Count(plain, "╭"), strings.Count(plain, "╰"); tops != bottoms {
		t.Errorf("%d boxes opened but %d closed — a bottom border fell off:\n%s", tops, bottoms, plain)
	}
}

// TestHelpOverlay_FitsAndScrolls — the help overlay was as tall as its
// text: at 80x24 the top (this screen's bindings) was cut off and there
// was no way to scroll. It must fit, start at the top, and scroll to the
// end with the arrows.
func TestHelpOverlay_FitsAndScrolls(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 20}} {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			a := newHomeApp(t, w, h, 3)
			a, _ = updateApp(t, a, keyMsg("?"))
			if !a.helpOpen {
				t.Fatal("? did not open help")
			}
			out := a.View()
			assertFrameFits(t, out, w, h)
			if !strings.Contains(ansi.Strip(out), "On this screen") {
				t.Errorf("help opened scrolled past its top:\n%s", ansi.Strip(out))
			}
			for i := 0; i < 60; i++ {
				a, _ = updateApp(t, a, keyMsg("down"))
			}
			out = a.View()
			assertFrameFits(t, out, w, h)
			if !strings.Contains(ansi.Strip(out), "ccmux contribute") {
				t.Errorf("scrolling down never reached the end of help:\n%s", ansi.Strip(out))
			}
			for i := 0; i < 60; i++ {
				a, _ = updateApp(t, a, keyMsg("up"))
			}
			if !strings.Contains(ansi.Strip(a.View()), "On this screen") {
				t.Error("scrolling back up didn't return to the top")
			}
		})
	}
	// A terminal tall enough shows everything with no scroll hint.
	a := newHomeApp(t, 120, 50, 3)
	a, _ = updateApp(t, a, keyMsg("?"))
	if out := ansi.Strip(a.View()); strings.Contains(out, "scroll") || !strings.Contains(out, "press ? or esc to close") {
		t.Errorf("tall terminal: help should fit without scrolling:\n%s", out)
	}
}

// TestTour_FitsSmallTerminals — tour slides were clipped at 40x20 (the
// second slide lost its top and bottom). Every slide must fit, and the
// arrows must scroll a slide that's taller than the terminal to its end.
func TestTour_FitsSmallTerminals(t *testing.T) {
	for _, size := range [][2]int{{40, 20}, {80, 24}} {
		w, h := size[0], size[1]
		a := newHomeApp(t, w, h, 0)
		a.tour.Open()
		for step := 0; step < len(a.tour.steps); step++ {
			t.Run(fmt.Sprintf("%dx%d/slide%d", w, h, step), func(t *testing.T) {
				assertFrameFits(t, a.View(), w, h)
				last := a.tour.steps[step]
				var tail string
				switch {
				case len(last.Bullets) > 0:
					tail = last.Bullets[len(last.Bullets)-1]
				default:
					tail = last.Body[len(last.Body)-1].Text
				}
				words := strings.Fields(tail)
				want := words[len(words)-1]
				for i := 0; i < 30 && !strings.Contains(ansi.Strip(a.View()), want); i++ {
					a, _ = updateApp(t, a, keyMsg("down"))
				}
				if !strings.Contains(ansi.Strip(a.View()), want) {
					t.Errorf("slide %d: its last line (%q) is unreachable:\n%s", step, want, ansi.Strip(a.View()))
				}
			})
			a, _ = updateApp(t, a, keyMsg("right"))
		}
	}
}

// TestAgentsAndSettings_KeepTheirFrame — at 80x24 and 60x24 the Agents
// and Settings panes were taller than the screen and lost their bottom
// border; below 120 columns the agent tab strip stacked one agent per row.
func TestAgentsAndSettings_KeepTheirFrame(t *testing.T) {
	for _, screen := range []Screen{ScreenAgents, ScreenSettings} {
		for _, size := range [][2]int{{80, 24}, {60, 24}, {100, 30}} {
			w, h := size[0], size[1]
			t.Run(fmt.Sprintf("%s/%dx%d", screen, w, h), func(t *testing.T) {
				a := newHomeApp(t, w, h, 0)
				a.screen = screen
				a.agentsM.SetSize(w, a.screenBodyHeight())
				out := a.View()
				assertFrameFits(t, out, w, h)
				if screen != ScreenAgents {
					return
				}
				// The tab strip: every agent tab present, on at most two rows.
				plain := strings.Split(ansi.Strip(out), "\n")
				rows := 0
				for _, l := range plain {
					if strings.Contains(l, "• Claude Code") || strings.Contains(l, "• Gemini CLI") || strings.Contains(l, "• Muse Code") {
						rows++
					}
				}
				if rows == 0 || rows > 2 {
					t.Errorf("agent tabs take %d rows, want 1-2:\n%s", rows, ansi.Strip(out))
				}
			})
		}
	}
}

// TestSettings_ScrollsWithCursor — with the list taller than its pane the
// selected row must stay visible as the cursor moves down.
func TestSettings_ScrollsWithCursor(t *testing.T) {
	a := newHomeApp(t, 80, 24, 0)
	a.screen = ScreenSettings
	fields := a.settings.fields()
	for i := 0; i < len(fields)-1; i++ {
		a, _ = updateApp(t, a, keyMsg("down"))
	}
	last := fields[len(fields)-1].label
	out := a.View()
	assertFrameFits(t, out, 80, 24)
	if !strings.Contains(ansi.Strip(out), last) {
		t.Errorf("selected last field %q scrolled out of view:\n%s", last, ansi.Strip(out))
	}
}

// TestNewSessionForm_FitsPhoneWidth — at 40x20 the new-session form's
// rows wrapped back under their labels and pushed the form off screen.
func TestNewSessionForm_FitsPhoneWidth(t *testing.T) {
	a := newHomeApp(t, 40, 20, 3)
	a, _ = updateApp(t, a, keyMsg("n"))
	if a.sessionsM.form == nil {
		t.Fatal("n did not open the new-session form")
	}
	out := a.View()
	assertFrameFits(t, out, 40, 20)
	plain := ansi.Strip(out)
	for _, label := range []string{"name", "working dir", "device", "agent"} {
		found := false
		for _, line := range strings.Split(plain, "\n") {
			if strings.Contains(line, label+" ") && strings.TrimSpace(strings.SplitN(line, label, 2)[1]) != "│" {
				found = true
			}
		}
		if !found {
			t.Errorf("row %q has no value on its own line:\n%s", label, plain)
		}
	}
}
