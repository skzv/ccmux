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

// globalHelp returns the keybindings that work on every screen.
// Rendered as the bottom section of the help overlay so per-screen
// listings stay tight and the user can see at a glance what's
// universal vs scoped.
func globalHelp(km Keymap) []HelpItem {
	first := km.Sessions.Keys()[0]
	last := km.Network.Keys()[0]
	switchHint := first + "-" + last + " / F" + first + "-F" + last
	return []HelpItem{
		{switchHint, tr("switch screens")},
		{"?", tr("this help")},
		{"T", tr("re-open the first-run tour")},
		{"M", tr("matrix 🐇")},
		{"esc", tr("dismiss toast")},
		{"q / Ctrl-c", tr("quit")},
	}
}

// helpForScreen returns the keybindings *specific to* `s`. These are
// merged with globalHelp() at render time into two labeled sections,
// so this list intentionally excludes anything in globalHelp.
//
// Each screen's bindings live here in one place rather than scattered
// across the screen files — it's easier to keep in sync with the
// actual implementation when there's a single source.
func helpForScreen(s Screen, km Keymap) []HelpItem {
	_ = km // currently unused for per-screen items; reserved for future per-screen-keymap variants
	switch s {
	case ScreenSessions:
		return []HelpItem{
			{"↑↓ / j k", tr("navigate session list")},
			{"enter", tr("attach (Ctrl-b then d to detach back to ccmux)")},
			{"n", tr("new session")},
			{"x", tr("kill selected session")},
			{"R", tr("rename selected session")},
			{"u", tr("open the full usage overlay")},
			{"r", tr("refresh sessions + usage")},
		}
	case ScreenProjects:
		return []HelpItem{
			{"↑↓ / j k", tr("navigate project list")},
			{"/", tr("filter projects by name (esc to clear, enter to attach to top match)")},
			{"enter", tr("attach to (or create) that project's session")},
			{"n", tr("scaffold a new project (modal form)")},
			{"a", tr("switch the selected project's agent (local only)")},
			{"i", tr("open the full project-info overlay")},
			{"c", tr("show conversations for this project")},
			{"r", tr("refresh projects + sessions")},
		}
	case ScreenConversations:
		return []HelpItem{
			{"↑↓ / j k", tr("navigate conversation list")},
			{"enter", tr("resume the selected conversation")},
			{"H", tr("toggle headless / SDK conversations")},
			{"r", tr("refresh conversation list")},
		}
	case ScreenNotes:
		return []HelpItem{
			{"p / space", tr("switch project (picker modal)")},
			{"tab / h / l / ←→", tr("toggle focus between list and preview")},
			{"↑↓ / j k (list focused)", tr("navigate files (wraps around)")},
			{"↑↓ / j k (preview focused)", tr("scroll within open doc")},
			{"mouse wheel", tr("scroll list (left) or doc (right)")},
			{"enter / e", tr("open selected file in $EDITOR")},
			{"n", tr("new note (asks for filename + optional title, then $EDITOR)")},
			{"i", tr("show selected note's info (path, frontmatter, counts)")},
			{"/", tr("search notes in this project")},
		}
	case ScreenAgents:
		return []HelpItem{
			{"m", tr("pick default model (modal)")},
			{"e", tr("pick reasoning effort (modal)")},
			{"a", tr("toggle alwaysThinkingEnabled on/off")},
			{"y", tr("toggle yolo mode (permissions.defaultMode = bypassPermissions)")},
			{"c", tr("edit global ~/.claude/CLAUDE.md in $EDITOR")},
			{"↑↓ / j k + enter", tr("activate a row (the settings.json row opens $EDITOR)")},
		}
	case ScreenSettings:
		return []HelpItem{
			{"↑↓ / j k", tr("navigate fields")},
			{"enter", tr("edit field (or cycle enum)")},
			{"esc", tr("cancel edit")},
			{"i", tr("open info modal (version, paths, last save)")},
			{"e", tr("open ~/.config/ccmux/config.toml in $EDITOR")},
		}
	case ScreenNetwork:
		return []HelpItem{
			{"↑↓ / j k", tr("navigate device list")},
			{"enter", tr("plain `ssh -t <host>` into the selected peer")},
			{"s", tr("open the SSH setup wizard for the focused host")},
			{"r", tr("refresh tailnet scan + ccmuxd probes")},
		}
	}
	return nil
}

// (renderHelpOverlay continues below; the toast no longer competes
// for the bottom help line — it floats at the top-right now, see
// app.View's toastRow insertion.)

// renderHelpOverlay produces the centered help modal. Two clearly
// labeled sections — "On this screen" (per-screen bindings) and
// "Anywhere" (globals) — followed by the recent toast log so a
// blink-past error can still be recalled.
func (a App) renderHelpOverlay(width, height int) string {
	st := a.styles
	screenName := a.screen.String()

	perScreen := helpForScreen(a.screen, a.keys)
	global := globalHelp(a.keys)

	// Pad the key column to the widest key across BOTH sections so
	// the two tables line up visually — otherwise the per-screen
	// section's narrow keys would left-align differently from the
	// globals' wider hints.
	maxKeyW := 0
	for _, it := range perScreen {
		if w := lipgloss.Width(it.Key); w > maxKeyW {
			maxKeyW = w
		}
	}
	for _, it := range global {
		if w := lipgloss.Width(it.Key); w > maxKeyW {
			maxKeyW = w
		}
	}

	lines := []string{
		st.Emphasis.Render(fmt.Sprintf("%s — %s", tr("Help"), screenName)),
		st.Subtitle.Render(tr("Bindings on this screen, then globals.")),
		"",
	}

	if len(perScreen) > 0 {
		lines = append(lines, st.Subtitle.Render(tr("On this screen")))
		for _, it := range perScreen {
			lines = append(lines, fmt.Sprintf("  %s   %s",
				st.Key.Render(padRight(it.Key, maxKeyW)),
				st.Muted.Render(it.Desc),
			))
		}
		lines = append(lines, "")
	}

	lines = append(lines, st.Subtitle.Render(tr("Anywhere")))
	for _, it := range global {
		lines = append(lines, fmt.Sprintf("  %s   %s",
			st.Key.Render(padRight(it.Key, maxKeyW)),
			st.Muted.Render(it.Desc),
		))
	}

	if log := a.toasts.Log(); len(log) > 0 {
		lines = append(lines, "", st.Subtitle.Render(tr("Recent activity")))
		for _, t := range log {
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
				st.Muted.Render(ago+" "+tr("ago")),
				color.Render(label),
			))
		}
	}

	lines = append(lines, "", st.Subtitle.Render(tr("Help improve ccmux")),
		st.Muted.Render(tr("Report issues, improve translations, or submit a PR.")),
		st.Muted.Render("ccmux contribute"))
	lines = append(lines, "", st.Muted.Render(tr("press ? or esc to close")))

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
