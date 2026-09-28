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
			{"/", tr("Search projects, previews, or IDs")},
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
	lines, modalW := a.helpLines(width)
	visible, footer := helpWindow(lines, a.helpScroll, height)
	if footer == "" {
		footer = tr("press ? or esc to close")
	}
	// One row, always: helpWindow budgeted exactly one for it.
	footer = truncate(footer, maxInt(1, modalW-2*st.Spacing.SM))
	body := strings.Join(append(visible, "", st.Muted.Render(footer)), "\n")
	modal := st.PaneFocused.Width(modalW).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

// helpLines builds the help text wrapped to the modal's text column for
// a width-column terminal, and the modal width it was wrapped for.
func (a App) helpLines(width int) ([]string, int) {
	st := a.styles
	screenName := a.screen.String()
	modalW := minInt(96, width-4)
	// The text column: the pane's Width includes its horizontal padding.
	textW := maxInt(1, modalW-2*st.Spacing.SM)

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
			lines = append(lines, helpRow(st.Key.Render(padRight(it.Key, maxKeyW)), it.Desc, st.Muted, textW)...)
		}
		lines = append(lines, "")
	}

	lines = append(lines, st.Subtitle.Render(tr("Anywhere")))
	for _, it := range global {
		lines = append(lines, helpRow(st.Key.Render(padRight(it.Key, maxKeyW)), it.Desc, st.Muted, textW)...)
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
			lines = append(lines, helpRow(st.Muted.Render(ago+" "+tr("ago")), label, color, textW)...)
		}
	}

	lines = append(lines, "", st.Subtitle.Render(tr("Help improve ccmux")),
		st.Muted.Render(tr("Report issues, improve translations, or submit a PR.")),
		st.Muted.Render("ccmux contribute"))
	// Wrap to the text column up front so the line count is what's drawn.
	return strings.Split(lipgloss.NewStyle().Width(textW).Render(strings.Join(lines, "\n")), "\n"), modalW
}

// helpChromeRows is what the help modal spends around its scrollable
// text: the border (top + bottom), the blank line and the footer.
const helpChromeRows = 4

// helpWindow picks the slice of the help text that fits a height-row
// terminal, starting at offset (clamped). When everything fits it
// returns all lines and an empty footer; otherwise the footer is a
// scroll hint with the position. The overlay used to be as tall as its
// text: on an 80x24 terminal the top of it (the screen's own bindings)
// was cut off with no way to scroll to it.
func helpWindow(lines []string, offset, height int) (visible []string, footer string) {
	room := height - helpChromeRows
	if room < 1 {
		room = 1
	}
	if len(lines) <= room {
		return lines, ""
	}
	maxOff := len(lines) - room
	offset = maxInt(0, minInt(offset, maxOff))
	return lines[offset : offset+room],
		fmt.Sprintf(tr("↑↓ scroll %d/%d · ? or esc to close"), offset+1, maxOff+1)
}

// helpScrollMax is the largest useful helpScroll for the current screen
// and terminal size — 0 when the help fits.
func (a App) helpScrollMax() int {
	lines, _ := a.helpLines(a.width)
	return maxInt(0, len(lines)-maxInt(1, a.height-helpChromeRows))
}

// helpRow lays out one "key   description" row in a textW-wide column.
// A description too long for the line wraps under itself (a hanging
// indent) instead of back under the key column.
func helpRow(key, desc string, descStyle lipgloss.Style, textW int) []string {
	prefix := "  " + key + "   "
	indent := lipgloss.Width(prefix)
	descW := textW - indent
	if descW < 12 {
		return []string{prefix + descStyle.Render(desc)}
	}
	wrapped := strings.Split(lipgloss.NewStyle().Width(descW).Render(desc), "\n")
	out := make([]string, len(wrapped))
	for i, l := range wrapped {
		l = strings.TrimRight(l, " ")
		if i == 0 {
			out[i] = prefix + descStyle.Render(l)
		} else {
			out[i] = strings.Repeat(" ", indent) + descStyle.Render(l)
		}
	}
	return out
}

func padRight(s string, n int) string {
	if w := lipgloss.Width(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}
