package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// renderScreenAt builds a minimal App focused on `screen` and renders
// the full frame (chrome + body) at the given dimensions. The width-
// sweep tests use it to check that no screen overflows its width and
// that the right content shows up at each width tier.
func renderScreenAt(screen Screen, width, height int) string {
	st := styles.Default()
	km := DefaultKeymap()
	a := App{
		styles:         st,
		keys:           km,
		screen:         screen,
		width:          width,
		height:         height,
		daemonOnline:   true,
		dashboard:      newDashboard(st, km),
		sessionsM:      newSessions(st, km),
		conversationsM: newConversations(st, km),
		projectsM:      newProjects(st, km),
		notes:          newNotes(st, km),
		agentsM:        newAgents(st, km),
		settings:       newSettings(st, km, config.Config{}, "test"),
		network:        newNetwork(st, km),
		matrix:         newMatrix(),
	}
	return a.View()
}

// assertNoOverflow fails the test if any line of `output` exceeds
// `width` display columns. lipgloss.Width strips ANSI escapes so a
// styled line is measured by what the terminal actually shows.
func assertNoOverflow(t *testing.T, output string, width int) {
	t.Helper()
	for i, line := range strings.Split(output, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("line %d overflows: %d > %d cols\n%q", i, w, width, line)
		}
	}
}

// assertPresent fails if any anchor string is missing from output.
// Used for T0 content that must survive every width.
func assertPresent(t *testing.T, output string, anchors ...string) {
	t.Helper()
	for _, a := range anchors {
		if !strings.Contains(output, a) {
			t.Errorf("expected %q in output but it was absent", a)
		}
	}
}

// assertAbsent fails if any anchor string appears in output. Used for
// T2 content that must be hidden below the narrow breakpoint.
func assertAbsent(t *testing.T, output string, anchors ...string) {
	t.Helper()
	for _, a := range anchors {
		if strings.Contains(output, a) {
			t.Errorf("expected %q to be absent but it was present", a)
		}
	}
}

// TestWidthSweep_AllScreens renders every screen (chrome + body) at a
// sweep of widths and asserts the screen width contract: no line
// overflows, T0 content is present at every width, and T2 content is
// absent below the 120 breakpoint. This is the regression net for the
// whole change — a new screen that overflows on a phone trips here.
func TestWidthSweep_AllScreens(t *testing.T) {
	widths := []int{50, 80, 100, 120, 200}
	cases := []struct {
		screen Screen
		t0     []string // must appear at every width
		t2     []string // must be absent below the 120 breakpoint
	}{
		{ScreenSessions, []string{"Sessions", "Claude usage"}, []string{"Hello.", "Welcome to ccmux", "Codex usage"}},
		{ScreenConversations, []string{"Conversations"}, nil},
		{ScreenProjects, []string{"Projects"}, nil},
		{ScreenNotes, []string{"Notes"}, nil},
		{ScreenAgents, []string{"Claude", "Codex", "Antigravity"}, []string{"switch agent"}},
		{ScreenSettings, []string{"Settings"}, []string{"config file"}},
		{ScreenNetwork, []string{"Network"}, []string{"every machine"}},
	}
	for _, tc := range cases {
		for _, w := range widths {
			t.Run(fmt.Sprintf("%s/%d", tc.screen, w), func(t *testing.T) {
				out := renderScreenAt(tc.screen, w, 44)
				assertNoOverflow(t, out, w)
				assertPresent(t, out, tc.t0...)
				if w < 120 {
					assertAbsent(t, out, tc.t2...)
				}
			})
		}
	}
}
