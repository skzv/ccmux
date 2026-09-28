package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/i18n"
	"github.com/skzv/ccmux/internal/tui/styles"
)

func TestTour_ZeroValueInactive(t *testing.T) {
	var m tourModel
	if m.Active() {
		t.Fatal("zero tourModel should be inactive")
	}
	if got := m.View(80, 24); got != "" {
		t.Fatalf("inactive tour should render empty; got %q", got[:min(40, len(got))])
	}
}

func TestTour_OpenAndClose(t *testing.T) {
	m := newTour(styles.Default())
	m.Open()
	if !m.Active() {
		t.Fatal("Open should activate")
	}
	if m.Step() != 0 {
		t.Errorf("Open should reset to step 0, got %d", m.Step())
	}
	m.Close()
	if m.Active() {
		t.Fatal("Close should deactivate")
	}
}

func TestTour_NextAdvancesUntilLast(t *testing.T) {
	m := newTour(styles.Default())
	m.Open()
	steps := len(m.steps)
	for i := 0; i < steps-1; i++ {
		if !m.Next() {
			t.Fatalf("Next at step %d should advance, returned false", m.Step())
		}
	}
	// We're now on the last step; Next must return false (signals "done").
	if m.Next() {
		t.Fatal("Next on the last step should return false (signals 'finished')")
	}
	// Step shouldn't have overflowed.
	if m.Step() != steps-1 {
		t.Errorf("after exhaustion step=%d, want %d", m.Step(), steps-1)
	}
}

func TestTour_PrevDoesNotGoBelowZero(t *testing.T) {
	m := newTour(styles.Default())
	m.Open()
	m.Prev()
	if m.Step() != 0 {
		t.Errorf("Prev at step 0 should stay at 0, got %d", m.Step())
	}
	m.Next()
	m.Prev()
	if m.Step() != 0 {
		t.Errorf("Next then Prev should land at 0, got %d", m.Step())
	}
}

func TestTour_ViewIncludesTitleAndKeyHint(t *testing.T) {
	m := newTour(styles.Default())
	m.Open()
	out := m.View(100, 30)
	if out == "" {
		t.Fatal("active tour should render non-empty")
	}
	first := m.steps[0]
	if !strings.Contains(out, first.Title) {
		t.Errorf("view missing title %q", first.Title)
	}
	if first.KeyHint != "" && !strings.Contains(out, first.KeyHint) {
		t.Errorf("view missing key hint %q", first.KeyHint)
	}
}

// TestDefaultTourSteps_ContainsRequiredAnchors locks in that the script
// hits the screens we promise in the README. If the tour ever drops one
// of the four main screens, this fires to force a conversation.
func TestDefaultTourSteps_ContainsRequiredAnchors(t *testing.T) {
	steps := defaultTourSteps()
	if len(steps) < 3 {
		t.Fatalf("tour has %d steps, expected at least 3", len(steps))
	}
	all := ""
	for _, s := range steps {
		all += s.Title + "\n" + tourBodyText(s) + "\n"
		for _, b := range s.Bullets {
			all += b + "\n"
		}
	}
	for _, must := range []string{"Sessions", "Projects", "Conversations", "Notes"} {
		if !strings.Contains(all, must) {
			t.Errorf("tour script missing reference to %q screen", must)
		}
	}
}

// TestTour_WelcomeCopyMatchesStepCount is the regression test for the
// welcome slide advertising a step count that drifted from reality
// ("This 5-step tour" while the tour had 4 slides). The count is now
// stamped in from len(steps), so this pins that the copy and the slice
// can never disagree again.
func TestTour_WelcomeCopyMatchesStepCount(t *testing.T) {
	steps := defaultTourSteps()
	if len(steps) == 0 {
		t.Fatal("tour has no steps")
	}
	welcome := tourBodyText(steps[0])
	want := fmt.Sprintf("This %d-step tour", len(steps))
	if !strings.Contains(welcome, want) {
		t.Errorf("welcome copy does not advertise the real step count: want substring %q in:\n%s", want, welcome)
	}
	if strings.Contains(welcome, "%d") {
		t.Errorf("count placeholder was not stamped in:\n%s", welcome)
	}
}

// TestTour_WelcomeParagraphsWrapAsAWhole — the welcome slide's copy was
// hard-broken into lines and each line wrapped on its own, so at 80 and
// 120 columns it came out ragged ("…Mosh, and" / "Tailscale." with room
// to spare on the line above). Paragraphs now wrap as a whole: no line
// ends early when the next line's first word would have fit on it.
func TestTour_WelcomeParagraphsWrapAsAWhole(t *testing.T) {
	withLang(t, "en")
	for _, code := range []string{"en", "de", "es", "fr", "ru"} {
		for _, w := range []int{40, 60, 80, 120} {
			t.Run(fmt.Sprintf("%s/%d", code, w), func(t *testing.T) {
				i18n.SetLanguage(code)
				defer i18n.SetLanguage("en")
				m := newTour(styles.Default())
				m.Open()
				l := m.layout(w, 60)
				textW := l.cardW - 2*l.padX
				// Skip the title and the blank line under it.
				body := l.body[2:]
				for i := 1; i < len(body); i++ {
					prev, next := ansi.Strip(body[i-1]), ansi.Strip(body[i])
					if strings.TrimSpace(prev) == "" || strings.TrimSpace(next) == "" {
						continue // paragraph break
					}
					first := strings.Fields(next)[0]
					if ansi.StringWidth(strings.TrimRight(prev, " "))+1+ansi.StringWidth(first) <= textW {
						t.Errorf("ragged wrap: %q ends early — %q fits after it (text width %d)", prev, first, textW)
					}
				}
			})
		}
	}
}

// TestTour_CommandsStayVerbatim — the remote-setup slide's commands
// were part of the translated text, so German read "ccmux-Host
// hinzufügen" for `ccmux host add`. The commands are printed as-is in
// every language.
func TestTour_CommandsStayVerbatim(t *testing.T) {
	withLang(t, "en")
	for _, code := range i18n.Codes() {
		i18n.SetLanguage(code)
		m := newTour(styles.Default())
		m.Open()
		m.step = len(m.steps) - 1
		out := ansi.Strip(m.View(120, 60))
		for _, cmd := range []string{"ccmux moshi-setup", "ccmux host add", "ccmuxd"} {
			if !strings.Contains(out, cmd) {
				t.Errorf("%s: slide lacks the command %q:\n%s", code, cmd, out)
			}
		}
	}
	i18n.SetLanguage("en")
}

// tourBodyText is a slide's body as plain text, one block per line
// (a command row as "cmd description").
func tourBodyText(s tourStep) string {
	lines := make([]string, len(s.Body))
	for i, p := range s.Body {
		lines[i] = strings.TrimSpace(p.Cmd + " " + p.Text)
	}
	return strings.Join(lines, "\n")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
