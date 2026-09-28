package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
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

// helpKey is the key a keymap binding is shown under, so the help
// names the key the binding actually uses.
func helpKey(b key.Binding) string { return b.Help().Key }

// helpForScreen returns the keybindings *specific to* `s`. These are
// merged with globalHelp() at render time into two labeled sections,
// so this list intentionally excludes anything in globalHelp.
//
// Each screen's bindings live here in one place rather than scattered
// across the screen files — it's easier to keep in sync with the
// actual implementation when there's a single source.
func helpForScreen(s Screen, km Keymap) []HelpItem {
	switch s {
	case ScreenSessions:
		return []HelpItem{
			{"↑↓ / j k", tr("navigate session list")},
			{"enter", tr("attach (Ctrl-b then d to detach back to ccmux)")},
			{helpKey(km.NewItem), tr("new session")},
			{helpKey(km.Preview), tr("toggle a live preview of the selected session")},
			{helpKey(km.Kill), tr("kill selected session")},
			{helpKey(km.Rename), tr("rename selected session")},
			{"u", tr("open the full usage overlay")},
			{helpKey(km.Refresh), tr("refresh sessions + usage")},
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
			{"tab / ←→ / h l", tr("move between the agent sections")},
			{"enter", tr("resume the selected conversation")},
			{helpKey(km.Preview), tr("preview the selected conversation's transcript")},
			{helpKey(km.Kill), tr("delete the selected conversation (press x twice to confirm)")},
			{helpKey(km.ToggleHeadless), tr("toggle headless / SDK conversations")},
			{"/", tr("Search projects, previews, or IDs")},
			{"esc", tr("clear the search, then the project filter")},
			{helpKey(km.Refresh), tr("refresh conversation list")},
		}
	case ScreenNotes:
		return []HelpItem{
			{"p / space", tr("switch project (picker modal)")},
			{"tab", tr("toggle focus between list and preview")},
			{"→ / l", tr("expand the folder (on a file: focus the preview)")},
			{"← / h", tr("collapse the folder or go to its parent (in the preview: back to the list)")},
			{"↑↓ / j k (list focused)", tr("navigate files (wraps around)")},
			{"↑↓ / j k (preview focused)", tr("scroll within open doc")},
			{"mouse wheel", tr("scroll list (left) or doc (right)")},
			{"enter / e", tr("open selected file in $EDITOR")},
			{"n", tr("new note (asks for filename + optional title, then $EDITOR)")},
			{"i", tr("show selected note's info (path, frontmatter, counts)")},
			{"/", tr("search notes in this project")},
			{"H", tr("switch device (read notes on another reachable machine)")},
		}
	case ScreenAgents:
		return []HelpItem{
			{"tab / l", tr("next agent")},
			{"shift+tab / h", tr("previous agent")},
			{"←→", tr("move focus between the list and the preview")},
			{"↑↓ / j k + enter", tr("activate a row (the settings.json row opens $EDITOR)")},
			// Per-agent keys, labelled with the agents they apply to
			// (the footer shows only the active agent's).
			{"m", "Claude: " + tr("pick default model (modal)")},
			{"e", "Claude: " + tr("pick reasoning effort (modal)")},
			{"a", "Claude: " + tr("toggle alwaysThinkingEnabled on/off")},
			{"y", "Claude: " + tr("toggle yolo mode (permissions.defaultMode = bypassPermissions)")},
			{"c", "Claude: " + tr("edit global ~/.claude/CLAUDE.md in $EDITOR")},
			{"r", "Codex, Antigravity: " + tr("pick reasoning effort (modal)")},
			{"y", "Codex, Antigravity: " + tr("toggle yolo mode")},
			{"e", "Codex, Antigravity, Gemini: " + tr("edit the agent's config file in $EDITOR")},
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
			{"i", tr("open the host-detail overlay")},
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
	visible, offset, maxOff := helpWindow(lines, a.helpScroll, height)
	// One row, always: helpWindow budgeted exactly one for it.
	footer := helpFooter(offset, maxOff, maxInt(1, modalW-2*st.Spacing.SM))
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
// terminal, starting at offset (clamped), and returns it with the
// clamped offset and the largest offset (0 when everything fits). The
// overlay used to be as tall as its text: on an 80x24 terminal the top
// of it (the screen's own bindings) was cut off with no way to scroll
// to it.
func helpWindow(lines []string, offset, height int) (visible []string, off, maxOff int) {
	room := height - helpChromeRows
	if room < 1 {
		room = 1
	}
	if len(lines) <= room {
		return lines, 0, 0
	}
	maxOff = len(lines) - room
	offset = maxInt(0, minInt(offset, maxOff))
	return lines[offset : offset+room], offset, maxOff
}

// helpFooter is the help modal's one-row footer for a textW-wide
// column: how to close it, plus the scroll position when the help
// scrolls. The longest form that fits wins; the close hint used to be
// cut off at 40 columns once the counter reached two digits ("↑↓ scroll
// 21/21 · ? or esc to clo…").
func helpFooter(offset, maxOff, textW int) string {
	var forms []string
	if maxOff == 0 {
		forms = []string{tr("press ? or esc to close"), tr("? / esc: close"), "esc"}
	} else {
		pos := []any{offset + 1, maxOff + 1}
		forms = []string{
			fmt.Sprintf(tr("↑↓ scroll %d/%d · ? or esc to close"), pos...),
			fmt.Sprintf(tr("↑↓ %d/%d · esc: close"), pos...),
			fmt.Sprintf("↑↓ %d/%d · esc", pos...),
			fmt.Sprintf("%d/%d esc", pos...),
		}
	}
	for _, f := range forms {
		if lipgloss.Width(f) <= textW {
			return f
		}
	}
	return truncate(forms[len(forms)-1], textW)
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
