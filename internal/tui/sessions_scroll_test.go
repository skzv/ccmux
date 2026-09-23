package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// newHomeApp is a fully-wired App on the Sessions (home) screen at a
// given terminal size, with n local sessions c-s00…c-sNN and two
// devices, delivered through the real sessionsLoadedMsg path so the
// dashboard tiles (Devices, Usage) render the way they do live.
func newHomeApp(t *testing.T, width, height, n int) App {
	t.Helper()
	st := styles.Default()
	km := DefaultKeymap()
	a := App{
		styles: st, keys: km, screen: ScreenSessions,
		width: width, height: height, daemonOnline: true,
		dashboard:      newDashboard(st, km),
		sessionsM:      newSessions(st, km),
		conversationsM: newConversations(st, km),
		projectsM:      newProjects(st, km),
		notes:          newNotes(st, km),
		agentsM:        newAgents(st, km),
		settings:       newSettings(st, km, config.Config{}, "test"),
		network:        newNetwork(st, km),
		tour:           newTour(st),
		matrix:         newMatrix(),
	}
	var ss []daemon.SessionState
	for i := 0; i < n; i++ {
		ss = append(ss, daemon.SessionState{Name: fmt.Sprintf("c-s%02d", i), Host: "local", State: "idle"})
	}
	a, _ = updateApp(t, a, sessionsLoadedMsg{
		Sessions: ss,
		Hosts: []hostStatus{
			{Name: "sputnik", Local: true, Source: "local", OK: true, DaemonOK: true},
			{Name: "mac-mini", Source: "configured", Address: "100.64.0.2:7474", OK: true, DaemonOK: true},
		},
		At: time.Now(),
	})
	return a
}

// TestSessionsList_ScrollsToCursor — the Sessions list never scrolled:
// with 25 sessions, moving to the last one left the selected row below
// the bottom of the screen. The list must window around the cursor at
// both the phone and the monitor layout, and the frame must still fit
// the terminal.
func TestSessionsList_ScrollsToCursor(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {140, 24}, {60, 30}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			a := newHomeApp(t, size.w, size.h, 25)
			a = navigate(t, a, 24)
			if got := selName(a.sessionsM.Selected()); got != "c-s24" {
				t.Fatalf("cursor on %s, want c-s24", got)
			}
			out := a.View()
			// The highlighted list row itself — not just the detail
			// pane's copy of the name — must be on screen.
			if !selectedRowShows(out, "c-s24") {
				t.Errorf("highlighted row for c-s24 is not on screen:\n%s", out)
			}
			if h := lipgloss.Height(out); h > size.h {
				t.Errorf("frame is %d lines, taller than the %d-line terminal", h, size.h)
			}
			// Back to the top: the window follows the cursor up again.
			a = navigateUp(t, a, 24)
			if out := a.View(); !selectedRowShows(out, "c-s00") {
				t.Errorf("after moving back up, the c-s00 row is not on screen:\n%s", out)
			}
		})
	}
}

// TestSessionsList_TilesStayVisible — at 80×24 the list grew with the
// session count and pushed the Devices and Usage tiles below it off
// the screen (Usage from 4 sessions, Devices from 7). The list is now
// budgeted to the space left over, so the tiles survive any count.
func TestSessionsList_TilesStayVisible(t *testing.T) {
	for _, n := range []int{0, 1, 4, 7, 10, 25} {
		t.Run(fmt.Sprintf("%d sessions", n), func(t *testing.T) {
			a := newHomeApp(t, 80, 24, n)
			out := a.View()
			for _, tile := range []string{"Usage", "Devices"} {
				if !strings.Contains(out, tile) {
					t.Errorf("%s tile vanished at 80×24 with %d sessions:\n%s", tile, n, out)
				}
			}
			if h := lipgloss.Height(out); h > 24 {
				t.Errorf("frame is %d lines, taller than the terminal", h)
			}
			assertNoOverflow(t, out, 80)
		})
	}
}

// TestSessionsList_RenderListFitsHeight pins the pane contract the
// layouts rely on: renderList never returns more lines than asked for,
// and always shows the selected row.
func TestSessionsList_RenderListFitsHeight(t *testing.T) {
	m := newSessions(styles.Default(), DefaultKeymap())
	var ss []daemon.SessionState
	for i := 0; i < 40; i++ {
		ss = append(ss, daemon.SessionState{Name: fmt.Sprintf("c-s%02d", i), Host: "local", State: "idle"})
	}
	m.SetSessions(ss)
	for _, h := range []int{4, 5, 6, 10, 23} {
		for _, cursor := range []int{0, 17, 39} {
			m.cursor = cursor
			out := m.renderList(60, h)
			if got := lipgloss.Height(out); got > h {
				t.Errorf("renderList(h=%d, cursor=%d) = %d lines", h, cursor, got)
			}
			if want := fmt.Sprintf("c-s%02d", cursor); !strings.Contains(out, want) {
				t.Errorf("renderList(h=%d) hides the selected row %s:\n%s", h, want, out)
			}
		}
	}
}

// selectedRowShows reports whether some rendered line carries the list
// selection bar (▌) together with name — i.e. the highlighted row is
// visible, as opposed to the name appearing only in a detail pane.
func selectedRowShows(out, name string) bool {
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "▌"); i >= 0 && strings.Contains(line[i:], name) {
			return true
		}
	}
	return false
}

func navigateUp(t *testing.T, a App, n int) App {
	t.Helper()
	for i := 0; i < n; i++ {
		a, _ = updateApp(t, a, keyRunes("k"))
	}
	return a
}
