package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// menuWithHistory builds the project menu for a project with no running
// session and n past conversations — the case where the cursor starts
// on the trailing "Start a new session" row.
func menuWithHistory(n int) projectMenuModel {
	convs := make([]conversations.Conversation, n)
	for i := range convs {
		convs[i] = conversations.Conversation{
			ID:      fmt.Sprintf("conv-%02d", i),
			Agent:   agent.IDClaude,
			Preview: fmt.Sprintf("conversation number %02d", i),
		}
	}
	return newProjectMenu(styles.Default(), "foo", "/p/foo", nil, convs)
}

// TestProjectMenu_ScrollsWithCursor — with 40 past conversations the
// modal rendered every row, outgrew the screen, and cut off the
// highlighted "Start a new session" row and the key-hint footer. The
// rows now window around the cursor inside the given height.
func TestProjectMenu_ScrollsWithCursor(t *testing.T) {
	m := menuWithHistory(40)
	for _, h := range []int{12, 20, 35} {
		for _, cursor := range []int{len(m.entries) - 1, 0, 17} {
			m.cursor = cursor
			out := m.View(76, h)
			if got := lipgloss.Height(out); got > h {
				t.Errorf("h=%d cursor=%d: menu is %d lines tall", h, cursor, got)
			}
			assertNoOverflow(t, out, 76)
			want := "Start a new session"
			if cursor < len(m.entries)-1 {
				want = fmt.Sprintf("conversation number %02d", cursor)
			}
			if !selectedRowShows(out, want) {
				t.Errorf("h=%d cursor=%d: highlighted row %q not visible:\n%s", h, cursor, want, out)
			}
			if !strings.Contains(out, "esc: cancel") {
				t.Errorf("h=%d cursor=%d: key-hint footer cut off:\n%s", h, cursor, out)
			}
		}
	}
}

// TestProjectMenu_FitsScreenThroughApp drives the real frame at 80×24:
// the menu opens on "Start a new session", and walking up to the first
// conversation keeps the highlighted row (and its section header) on
// screen.
func TestProjectMenu_FitsScreenThroughApp(t *testing.T) {
	a := newHomeApp(t, 80, 24, 0)
	a.screen = ScreenProjects
	menu := menuWithHistory(40)
	a.projectsM.menu = &menu

	out := a.View()
	if !selectedRowShows(out, "Start a new session") || !strings.Contains(out, "esc: cancel") {
		t.Fatalf("initial menu cut off the highlighted row or footer:\n%s", out)
	}
	for i := 0; i < 40; i++ {
		a, _ = updateApp(t, a, tea.KeyMsg{Type: tea.KeyUp})
	}
	out = a.View()
	if !selectedRowShows(out, "conversation number 00") || !strings.Contains(out, "Past conversations") {
		t.Fatalf("after scrolling to the top, first conversation / header not visible:\n%s", out)
	}
	if h := lipgloss.Height(out); h > 24 {
		t.Errorf("frame is %d lines tall at 24 rows", h)
	}
}
