package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/tui/components"
)

// View renders the whole UI.
// screenBodyHeight mirrors App.View's body budget for the active
// screen: terminal height minus the rendered chrome rows (header,
// status bar, help line). The transient toast row is deliberately
// ignored — it shifts the budget by ~3 lines for a few seconds, which
// is fine for the Update-side size consumers (the Agents browsers'
// persistent viewports) that only need the steady-state rectangle.
func (a App) screenBodyHeight() int {
	chromeH := lipgloss.Height(a.renderHeader()) +
		lipgloss.Height(a.renderStatusBar()) +
		lipgloss.Height(a.renderHelpLine())
	h := a.height - chromeH
	if h < 5 {
		h = 5
	}
	return h
}

func (a App) View() string {
	if a.width == 0 {
		return "loading…"
	}
	// Attach-loading overlay takes the full screen between Enter and
	// the tmux suspend. Placed before the regular chrome render so
	// we don't waste work building panels the user won't see, and
	// before the matrix overlay because attach is task-critical and
	// shouldn't be hidden by the easter egg.
	if a.attach.active {
		return a.renderAttachingOverlay(a.width, a.height)
	}
	// Matrix easter egg takes the full screen when active — placed
	// before the chrome render so we don't waste work building the
	// header/footer just to throw them away.
	if a.matrix.Active() {
		return a.matrix.View(a.width, a.height)
	}

	header := a.renderHeader()
	statusBar := a.renderStatusBar()
	helpLine := a.renderHelpLine()

	// Bottom-right toast bubble. Rendered as a rounded-border Charm
	// box (toasts.Render) and placed flush against the right edge
	// directly above the status bar. The body shrinks by the bubble
	// height while the toast is alive — columns stay anchored at
	// the top so they don't visibly reflow when a toast appears.
	// The existing 2s tick re-renders the frame so the toast
	// disappears on its own when `until` passes.
	//
	// Suppressed on wide Conversations: that screen routes the same
	// toast into its detail-pane banner (see body switch below for
	// the SetBanner call), and we don't want to render it twice.
	var toastRow string
	toastH := 0
	conversationsSideToast := a.toasts.Active() &&
		a.screen == ScreenConversations && !isNarrow(a.width)
	if a.toasts.Active() && !conversationsSideToast {
		// Capped to the terminal (less a column each side) so a long
		// message wraps inside the bubble instead of being truncated.
		toastRow = lipgloss.PlaceHorizontal(a.width, lipgloss.Right, a.toasts.Render(a.styles, a.width-2))
		toastH = lipgloss.Height(toastRow)
	}

	// Measure each chrome row's actual rendered height so we never
	// budget too generously and let body content push the header off
	// the top. lipgloss.Height counts \n's + 1 so it includes any
	// invisible line breaks even if forceSingleLine didn't get them.
	chromeH := lipgloss.Height(header) + lipgloss.Height(statusBar) + lipgloss.Height(helpLine) + toastH
	bodyHeight := a.height - chromeH
	if bodyHeight < 5 {
		bodyHeight = 5
	}

	var body string
	switch a.screen {
	case ScreenSessions:
		body = a.homeView(a.width, bodyHeight)
	case ScreenConversations:
		// Wide layout swaps the screen footer toast for an inline
		// banner at the top of the detail pane — closer to the action
		// that produced the notification.
		if !isNarrow(a.width) && a.toasts.Active() {
			// The banner sits inside the detail pane: half the width,
			// less the pane's border and padding.
			a.conversationsM.SetBanner(a.toasts.Render(a.styles, a.width/2-4))
		} else {
			a.conversationsM.SetBanner("")
		}
		body = a.conversationsM.View(a.width, bodyHeight)
	case ScreenProjects:
		body = a.projectsM.View(a.width, bodyHeight)
	case ScreenNotes:
		body = a.notes.View(a.width, bodyHeight)
	case ScreenAgents:
		body = a.agentsM.View(a.width, bodyHeight)
	case ScreenSettings:
		body = a.settings.View(a.width, bodyHeight)
	case ScreenNetwork:
		body = a.network.View(a.width, bodyHeight)
	}
	// Defensive clamp + pad: never let the body exceed its budget
	// (otherwise it would push the status bar / help line off the
	// bottom), AND never let it return SHORTER than its budget (or
	// the status bar / help line float up and leave blank rows under
	// the help hints — visible on screens like Agents whose body
	// doesn't wrap in a height-padded Pane).
	body = clampLines(body, bodyHeight)
	body = padToHeight(body, bodyHeight)

	parts := []string{header, body}
	if toastRow != "" {
		parts = append(parts, toastRow)
	}
	parts = append(parts, statusBar, helpLine)
	frame := lipgloss.JoinVertical(lipgloss.Left, parts...)
	// Overlay precedence mirrors App.Update input routing:
	// confirm > sshWizard > tour > help > usage > convPreview >
	// projectInfo > settingsInfo > network.Detail > regular frame.
	if a.confirm.open() {
		return a.renderConfirmationOverlay(a.width, a.height)
	}
	if a.sshWizard != nil && a.sshWizard.Active() {
		return a.sshWizard.View(a.width, a.height)
	}
	if a.tour.Active() {
		return a.tour.View(a.width, a.height)
	}
	if a.helpOpen {
		return a.renderHelpOverlay(a.width, a.height)
	}
	if a.usageOpen {
		return a.dashboard.renderUsageOverlay(a.styles, a.width, a.height)
	}
	if a.convPreview.IsOpen() {
		return a.convPreview.View(a.styles, a.width, a.height)
	}
	if a.projectInfoOpen {
		if sel := a.projectsM.Selected(); sel != nil {
			return projectInfoOverlay{}.View(a.styles, *sel, a.sessions, a.width, a.height)
		}
		// Selection vanished between open and render (unlikely race);
		// fall through to the base frame. The next keypress will see
		// projectInfoOpen=true and close it via the "esc"/"i" handler.
	}
	if a.settingsInfoOpen {
		return a.settings.renderSettingsInfoOverlay(a.width, a.height)
	}
	if a.network.DetailOpen() {
		return a.network.renderDetailOverlay(a.width, a.height)
	}
	return frame
}

// padToHeight extends `s` with trailing blank lines so its line
// count is at least `n`. Screens whose body doesn't wrap in a
// height-padded Pane (Agents, the per-agent config sub-screens)
// otherwise leave blank rows under the help line because the chrome
// below body floats up. No-op when `s` already has >= n lines.
func padToHeight(s string, n int) string {
	if n <= 0 {
		return s
	}
	have := lipgloss.Height(s)
	if have >= n {
		return s
	}
	return s + strings.Repeat("\n", n-have)
}

// clampLines returns the first `n` lines of `s` verbatim. Preserves the
// internal newline format. Returns `s` unchanged if it already fits.
func clampLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			count++
			if count == n {
				return s[:i]
			}
		}
	}
	return s
}

// homeView renders the combined Home screen. On a phone (narrow) the
// hero is dropped entirely and the screen is a single column: sessions,
// then the stat tiles. On a monitor the hero is a full-width banner
// across the top; below it the sessions list on the left, and the
// session detail + the three stat tiles stacked on the right.
func (a App) homeView(width, height int) string {
	narrow := isNarrow(width)
	a.dashboard.narrow = narrow // a is a value copy; the panels read m.narrow

	// A sessions modal (rename / new-session form) takes the whole
	// Home body, centered — sessionsModel.View owns that rendering.
	if a.sessionsM.form != nil || a.sessionsM.renameForm != nil {
		return a.sessionsM.View(width, height, narrow)
	}

	if narrow {
		// No hero on a phone — straight to sessions, then the tiles.
		tiles := a.dashboard.StatsView(width)
		listH := height - lipgloss.Height(tiles)
		if listH < 5 {
			listH = 5
		}
		sessions := a.sessionsM.View(width, listH, true)
		return lipgloss.JoinVertical(lipgloss.Left, sessions, tiles)
	}

	// Monitor: hero banner across the top; below it the sessions list
	// on the left and the session detail + stat tiles on the right.
	hero := a.dashboard.heroPanel(width)
	rowH := height - lipgloss.Height(hero)
	if rowH < 5 {
		rowH = 5
	}
	gutter := 1
	leftW := (width - gutter) / 2
	rightW := width - leftW - gutter
	left := a.sessionsM.renderList(leftW, rowH)
	// Preview mode (`p`): the right column becomes a live, read-only
	// view of the selected session's pane instead of the detail + stat
	// tiles. When you're watching an agent work, the tiles are noise —
	// the preview earns the whole column. The render path MUST live
	// here (not in sessionsModel.View) because the wide Home screen
	// composes its own layout and never calls sessionsModel.View — the
	// original bug was putting the split in View, which this screen
	// bypasses entirely.
	var right string
	if a.sessionsM.showPreview {
		right = a.sessionsM.renderPreview(rightW, rowH)
	} else {
		right = lipgloss.JoinVertical(lipgloss.Left,
			a.sessionsM.renderDetail(rightW, false),
			a.dashboard.StatsView(rightW),
		)
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	return lipgloss.JoinVertical(lipgloss.Left, hero, row)
}

// renderHeader is the top-of-screen tab strip. On narrow terminals the
// tab labels collapse to just their number; the full strip never wraps.
//
// The tab list comes from allScreens() — never a hardcoded slice.
// A hardcoded list is what dropped the Conversations tab from the bar
// for a release; deriving from the Screen enum makes that class of
// bug structurally impossible.
//
// Narrow threshold for the HEADER specifically is computed from the
// actual label lengths (tabBarMinWidth) — the bar collapses only
// when the wide form genuinely wouldn't fit, not at the wider
// isNarrow(120) cutoff content layouts use. This way a viewer at
// ~110 cols still sees `[1] Sessions [2] Projects …` instead of the
// bare numeric form, which several README GIFs were burning into
// muscle memory as the "always" look.
func (a App) renderHeader() string {
	narrow := a.width < tabBarMinWidth()
	var parts []string
	// The " ccmux " brand title is T2 — dropped on narrow so the tab
	// numbers get the reclaimed width.
	if !narrow {
		parts = append(parts, a.styles.Title.Render(" ccmux "))
	}
	for _, t := range allScreens() {
		// Number label = enum value + 1. This is the SAME number the
		// keymap binds (Dashboard→1 … Network→8), because the keymap
		// and the enum share their order. Using int(t)+1 rather than a
		// loop index keeps the label correct even if allScreens() is
		// ever reordered.
		num := int(t) + 1
		var label string
		if narrow {
			// Just the number when space is tight.
			label = fmt.Sprintf(" %d ", num)
			if t == a.screen {
				// First rune, not first byte: labels are translated and
				// "会话"[0] would split a UTF-8 sequence.
				label = fmt.Sprintf("[%d %s]", num, string([]rune(t.String())[:1]))
			}
		} else {
			label = fmt.Sprintf("[%d] %s", num, t.String())
			label = " " + label + " "
		}
		if t == a.screen {
			parts = append(parts, a.styles.TabActive.Render(label))
		} else {
			parts = append(parts, a.styles.TabInactive.Render(label))
		}
	}
	line := lipgloss.NewStyle().Background(a.styles.P.BGAlt).Render(strings.Join(parts, ""))
	return forceSingleLine(line, a.width)
}

// renderStatusBar is the bottom-most informational strip. Forced to 1 line.
// On narrow terminals the right-side details are dropped first.
func (a App) renderStatusBar() string {
	narrow := isNarrow(a.width)

	host, _ := os.Hostname()
	hostChip := a.styles.HostColor("local").Render("● " + shortHostname(host))

	daemonChip := a.styles.StatusError.Render("⚠ " + tr("offline"))
	if a.daemonOnline {
		daemonChip = a.styles.StatusGood.Render("✓ " + tr("daemon"))
	}

	dangerBanner := ""
	if a.cfg.Sleep.DangerousKeepAwakeOnBattery {
		dangerBanner = a.styles.StatusDanger.Render("⚠ "+tr("BATT")) + " "
	}

	// Left block ordered T0-first — battery-danger, daemon, then host
	// — so if forceSingleLine still has to truncate it eats the lower-
	// priority host before the safety-critical chips.
	leftBlock := dangerBanner + daemonChip + "  " + hostChip

	// Right block: the session count (T1) always; the refreshed-at
	// clock and the version chip (both T2) only when wide. Dirty
	// builds (`<sha>-dirty`) still self-flag in the wide version chip.
	right := a.styles.Muted.Render(fmt.Sprintf(tr("%d sess"), len(a.sessions)))
	if !narrow {
		refreshed := "—"
		if !a.lastRefresh.IsZero() {
			refreshed = a.lastRefresh.Format("15:04:05")
		}
		versionChip := a.styles.Muted.Render(versionLabel(a.version))
		if strings.Contains(a.version, "dirty") {
			versionChip = a.styles.StatusWarning.Render(a.version)
		}
		right = a.styles.Muted.Render(fmt.Sprintf(tr("%d sess • %s"), len(a.sessions), refreshed)) + "  " + versionChip
	}

	// Local ccmux update chip — appended to the right block when the
	// launch-time self-update check found this checkout behind upstream.
	// Replaces the old hero-panel update banner so the chrome doesn't
	// compete with screen content for vertical space.
	if r := a.dashboard.updateAvailable; r.Available() {
		commits := tr("1 commit")
		if r.Behind != 1 {
			commits = fmt.Sprintf(tr("%d commits"), r.Behind)
		}
		updateChip := lipgloss.NewStyle().Foreground(a.styles.P.Yellow).Bold(true).
			Render(fmt.Sprintf(tr("[↑ ccmux update — %s behind %s]"), commits, r.Branch))
		right = right + "  " + updateChip
	}

	// Compute available space for the right block. The StatusBar style
	// adds 1-col padding each side, so the composed body must target
	// width-2 — otherwise forceSingleLine would chop the right tail
	// (the version chip). If the left side already overflows we skip
	// the right block rather than feeding strings.Repeat a negative.
	inner := a.width - 2
	leftW := lipgloss.Width(leftBlock)
	rightW := lipgloss.Width(right)
	body := leftBlock
	if inner-leftW-rightW >= 2 {
		spacer := strings.Repeat(" ", inner-leftW-rightW)
		body = leftBlock + spacer + right
	}
	line := a.styles.StatusBar.Render(body)
	return forceSingleLine(line, a.width)
}

// versionLabel is the status bar's version chip: a "v" prefix for bare
// release numbers, none for `git describe` output that already has one
// (make builds showed "vv0.6.1-12-g…").
func versionLabel(v string) string {
	if v == "" || strings.HasPrefix(v, "v") || strings.HasPrefix(v, "V") {
		return v
	}
	if v[0] < '0' || v[0] > '9' {
		return v // "dev", a bare sha: not a version number
	}
	return "v" + v
}

// renderHelpLine renders the screen's bottom help row. The line is
// the migrated components.HelpBar driven by the current screen's
// HelpBarProps (or a generic fallback for unmigrated screens). The
// toast no longer competes for this slot — it floats at the top-
// right (see View's toastRow), so the keybinds are always visible.
func (a App) renderHelpLine() string {
	return forceSingleLine(components.HelpBar(a.styles, a.helpBarProps()), a.width)
}

// helpBarProps returns the components.HelpBar contract for the active
// screen. Migrated screens drive their own; unmigrated screens get
// the generic global hint set as a fallback.
func (a App) helpBarProps() components.HelpBarProps {
	switch a.screen {
	case ScreenSessions:
		return a.dashboard.HelpBarProps(a.width)
	case ScreenProjects:
		return a.projectsM.HelpBarProps(a.width)
	case ScreenConversations:
		return a.conversationsM.HelpBarProps(a.width)
	case ScreenNotes:
		return a.notes.HelpBarProps(a.width)
	case ScreenSettings:
		return a.settings.HelpBarProps(a.width)
	case ScreenAgents:
		return a.agentsM.HelpBarProps(a.width)
	case ScreenNetwork:
		return a.network.HelpBarProps(a.width)
	default:
		return components.HelpBarProps{
			Hints: []components.KeyHint{
				{Key: "?", Label: tr("help"), Priority: 10},
				{Key: "q", Label: tr("quit"), Priority: 10},
				{Key: "r", Label: tr("refresh"), Priority: 6},
				{Key: "1-7", Label: tr("screens"), Priority: 4},
			},
			Width: a.width,
		}
	}
}

// forceSingleLine guarantees the rendered string is exactly one line tall
// and at most `width` *display* cells wide. Uses ansi.Truncate so styled
// content (ANSI escape sequences) is preserved correctly — a plain
// rune-slice would happily chop a sequence in half and corrupt the
// terminal state.
func forceSingleLine(s string, width int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

// shortHostname strips trailing ".local" / ".lan" / tailnet suffix for the
// status bar so "sputnik.mini.skz.dev" becomes "sputnik".
func shortHostname(h string) string {
	if i := strings.IndexByte(h, '.'); i > 0 {
		return h[:i]
	}
	return h
}
