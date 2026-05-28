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
		{switchHint, "switch screens"},
		{"?", "this help"},
		{"T", "re-open the first-run tour"},
		{"M", "matrix 🐇"},
		{"esc", "dismiss toast"},
		{"q / Ctrl-c", "quit"},
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
			{"↑↓ / j k", "navigate session list"},
			{"enter", "attach (Ctrl-b then d to detach back to ccmux)"},
			{"n", "new session"},
			{"x", "kill selected session"},
			{"R", "rename selected session"},
			{"u", "open the full usage overlay"},
			{"r", "refresh sessions + usage"},
		}
	case ScreenProjects:
		return []HelpItem{
			{"↑↓ / j k", "navigate project list"},
			{"/", "filter projects by name (esc to clear, enter to attach to top match)"},
			{"enter", "attach to (or create) that project's session"},
			{"n", "scaffold a new project (modal form)"},
			{"a", "switch the selected project's agent (local only)"},
			{"i", "open the full project-info overlay"},
			{"c", "show conversations for this project"},
			{"r", "refresh projects + sessions"},
		}
	case ScreenConversations:
		return []HelpItem{
			{"↑↓ / j k", "navigate conversation list"},
			{"enter", "resume the selected conversation"},
			{"H", "toggle headless / SDK conversations"},
			{"r", "refresh conversation list"},
		}
	case ScreenNotes:
		return []HelpItem{
			{"p", "switch project (picker modal)"},
			{"tab", "toggle focus between list and preview"},
			{"↑↓ / j k (list focused)", "navigate files"},
			{"↑↓ / j k (preview focused)", "scroll within open doc"},
			{"enter / e", "open selected file in $EDITOR"},
		}
	case ScreenAgents:
		return []HelpItem{
			{"m", "pick default model (modal)"},
			{"e", "pick reasoning effort (modal)"},
			{"a", "toggle alwaysThinkingEnabled on/off"},
			{"y", "toggle yolo mode (permissions.defaultMode = bypassPermissions)"},
			{"c", "edit global ~/.claude/CLAUDE.md in $EDITOR"},
			{"j", "edit ~/.claude/settings.json directly"},
		}
	case ScreenSettings:
		return []HelpItem{
			{"(read-only for now)", "edit ~/.config/ccmux/config.toml manually"},
		}
	case ScreenNetwork:
		return []HelpItem{
			{"↑↓ / j k", "navigate device list"},
			{"enter", "plain `ssh -t <host>` into the selected peer"},
			{"s", "open the SSH setup wizard for the focused host"},
			{"r", "refresh tailnet scan + ccmuxd probes"},
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
		st.Emphasis.Render("Help — " + screenName),
		st.Subtitle.Render("Bindings on this screen, then globals."),
		"",
	}

	if len(perScreen) > 0 {
		lines = append(lines, st.Subtitle.Render("On this screen"))
		for _, it := range perScreen {
			lines = append(lines, fmt.Sprintf("  %s   %s",
				st.Key.Render(padRight(it.Key, maxKeyW)),
				st.Muted.Render(it.Desc),
			))
		}
		lines = append(lines, "")
	}

	lines = append(lines, st.Subtitle.Render("Anywhere"))
	for _, it := range global {
		lines = append(lines, fmt.Sprintf("  %s   %s",
			st.Key.Render(padRight(it.Key, maxKeyW)),
			st.Muted.Render(it.Desc),
		))
	}

	if log := a.toasts.Log(); len(log) > 0 {
		lines = append(lines, "", st.Subtitle.Render("Recent activity"))
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
