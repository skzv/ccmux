package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// HelpItem is one row in the per-screen keybinding overlay.
type HelpItem struct {
	Key  string
	Desc string
}

// helpForScreen returns the contextual key reference for `s`. Each
// screen's bindings live here in one place rather than scattered
// across the screen files — it's easier to keep in sync with the
// actual implementation when there's a single source.
func helpForScreen(s Screen) []HelpItem {
	common := []HelpItem{
		{"1-7 / F1-F7", "switch screens"},
		{"r", "refresh now"},
		{"?", "this help"},
		{"T", "re-open the first-run tour"},
		{"M", "matrix 🐇"},
		{"esc", "dismiss toast"},
		{"q / Ctrl-c", "quit"},
	}
	switch s {
	case ScreenDashboard:
		return append([]HelpItem{
			{"r", "refresh sessions + usage now"},
		}, common...)
	case ScreenSessions:
		return append([]HelpItem{
			{"↑↓ / j k", "navigate session list"},
			{"enter", "attach (Ctrl-b then d to detach back to ccmux)"},
			{"x", "kill selected session"},
			{"R", "rename selected session"},
			{"k", "toggle keep-awake (coming soon)"},
			{"s", "snapshot session (coming soon)"},
		}, common...)
	case ScreenProjects:
		return append([]HelpItem{
			{"↑↓ / j k", "navigate project list"},
			{"/", "filter projects by name (esc to clear, enter to attach to top match)"},
			{"enter", "attach to (or create) that project's session"},
			{"n", "scaffold a new project (modal form)"},
			{"u", "upgrade cwd with ccmux structure"},
			{"a", "switch the selected project's agent (local only)"},
		}, common...)
	case ScreenNotes:
		return append([]HelpItem{
			{"p", "switch project (picker modal)"},
			{"tab", "toggle focus between list and preview"},
			{"↑↓ / j k (list focused)", "navigate files"},
			{"↑↓ / j k (preview focused)", "scroll within open doc"},
			{"n", "new note picker (Agent Log / Spec / ADR)"},
			{"e", "open selected file in $EDITOR"},
		}, common...)
	case ScreenAgents:
		return append([]HelpItem{
			{"m", "pick default model (modal)"},
			{"e", "pick reasoning effort (modal)"},
			{"a", "toggle alwaysThinkingEnabled on/off"},
			{"y", "toggle yolo mode (permissions.defaultMode = bypassPermissions)"},
			{"c", "edit global ~/.claude/CLAUDE.md in $EDITOR"},
			{"j", "edit ~/.claude/settings.json directly"},
		}, common...)
	case ScreenSettings:
		return append([]HelpItem{
			{"(read-only for now)", "edit ~/.config/ccmux/config.toml manually"},
		}, common...)
	case ScreenNetwork:
		return append([]HelpItem{
			{"↑↓ / j k", "navigate device list"},
			{"enter", "plain `ssh -t <host>` into the selected peer"},
			{"r", "refresh tailnet scan + ccmuxd probes"},
		}, common...)
	}
	return common
}

// renderHelpOverlay produces the centered help modal showing the
// current screen's keybindings plus the most recent toasts (so a
// blink-past error can be recalled).
func (a App) renderHelpOverlay(width, height int) string {
	st := a.styles
	screenName := a.screen.String()

	lines := []string{
		st.Emphasis.Render("Help — " + screenName),
		st.Subtitle.Render("Per-screen bindings + recent activity."),
		"",
	}
	maxKeyW := 0
	items := helpForScreen(a.screen)
	for _, it := range items {
		if w := lipgloss.Width(it.Key); w > maxKeyW {
			maxKeyW = w
		}
	}
	for _, it := range items {
		lines = append(lines, fmt.Sprintf("  %s   %s",
			st.Key.Render(padRight(it.Key, maxKeyW)),
			st.Muted.Render(it.Desc),
		))
	}

	if len(a.toastLog) > 0 {
		lines = append(lines, "", st.Subtitle.Render("Recent activity"))
		for _, t := range a.toastLog {
			label := t.Text
			color := st.Muted
			switch t.Kind {
			case toastError:
				color = st.StatusError
			case toastSuccess:
				color = st.StatusGood
			case toastWarning:
				color = st.StatusWarning
			}
			ago := humanDuration(time.Since(t.At))
			lines = append(lines, fmt.Sprintf("  %s   %s",
				st.Muted.Render(ago+" ago"),
				color.Render(label),
			))
		}
	}

	lines = append(lines, "", st.Muted.Render("press ? or esc to close"))

	modalW := minInt(96, width-4)
	body := strings.Join(lines, "\n")
	modal := st.PaneFocused.Width(modalW).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

func padRight(s string, n int) string {
	if w := lipgloss.Width(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}
