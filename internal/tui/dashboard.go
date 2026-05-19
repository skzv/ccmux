package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/claudeusage"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tui/styles"
	"github.com/skzv/ccmux/internal/usage"
)

// dashboardModel renders the at-a-glance landing screen.
// On wide terminals: hero + (sessions list | stats + usage).
// On narrow terminals (< 80 cols): everything stacked vertically.
type dashboardModel struct {
	st       styles.Styles
	km       Keymap
	sessions []daemon.SessionState
	hosts    []hostStatus
	version  string // this build's ccmux version, for the device-network panel
	cfg      config.Config
	usage    *claudeusage.Aggregate
	usageAt  time.Time

	// Cross-agent token-usage summaries pushed by App on every
	// usageLoadedMsg. Codex/Antigravity are zero-valued today (stub
	// walkers; see internal/usage); the renderer keys off HasData
	// to decide between "real numbers" and "install hint".
	codexUsage       usage.AgentSummary
	antigravityUsage usage.AgentSummary
}

func newDashboard(st styles.Styles, km Keymap) dashboardModel {
	return dashboardModel{st: st, km: km, cfg: config.Defaults()}
}

// SetConfig propagates the user config so the usage panel can pick up
// subscription tier and dashboard preferences.
func (m *dashboardModel) SetConfig(cfg config.Config) {
	m.cfg = cfg
}

// SetUsage receives a freshly-computed Aggregate. The App schedules
// these on a slower cadence than the session refresh so we don't walk
// the transcript tree every 2 seconds.
func (m *dashboardModel) SetUsage(a *claudeusage.Aggregate) {
	m.usage = a
	m.usageAt = time.Now()
}

// SetCodexUsage / SetAntigravityUsage receive cross-agent summaries
// from the App's usage refresh. Today both walkers stub out; the
// renderer shows an install-hint placeholder when HasData=false.
func (m *dashboardModel) SetCodexUsage(s usage.AgentSummary)       { m.codexUsage = s }
func (m *dashboardModel) SetAntigravityUsage(s usage.AgentSummary) { m.antigravityUsage = s }

func (m dashboardModel) Update(msg tea.Msg) (dashboardModel, tea.Cmd) {
	return m, nil
}

func (m *dashboardModel) SetSessions(ss []daemon.SessionState) {
	m.sessions = ss
}

// SetHosts receives the per-refresh list of local + configured-remote +
// auto-discovered ccmuxd hosts so the device-network panel can render
// versions and "update available" flags.
func (m *dashboardModel) SetHosts(hs []hostStatus) {
	m.hosts = hs
}

// SetVersion gives the dashboard this build's own ccmux version so the
// device-network panel can compare against remote ccmuxds.
func (m *dashboardModel) SetVersion(v string) {
	m.version = v
}

func (m dashboardModel) View(width, height int) string {
	if isNarrow(width) {
		return m.viewNarrow(width, height)
	}
	return m.viewWide(width, height)
}

func (m dashboardModel) viewWide(width, height int) string {
	hero := m.heroPanel(width)
	heroH := lipgloss.Height(hero)
	rowH := height - heroH
	if rowH < 8 {
		rowH = 8
	}

	// Layout: left column carries Sessions, right column carries the
	// stack of Session-summary + Devices + Usage. The previous 2:1
	// split made Sessions dominant and squeezed the Devices panel,
	// which has the most information density per row (name + version
	// + tags + warnings). Reweighting to 1:1 (with a 1-col gutter)
	// gives Devices room to breathe; Sessions still fits comfortably
	// since most rows are short.
	gutter := 1
	leftW := (width - gutter) / 2
	rightW := width - leftW - gutter

	sessions := m.topSessions(leftW, rowH)

	stats := m.statsPanel(rightW)
	devices := m.devicesPanel(rightW)
	usage := m.usagePanel(rightW)
	right := lipgloss.JoinVertical(lipgloss.Left, stats, devices, usage)

	row := lipgloss.JoinHorizontal(lipgloss.Top, sessions, " ", right)
	return lipgloss.JoinVertical(lipgloss.Left, hero, row)
}

func (m dashboardModel) viewNarrow(width, height int) string {
	hero := m.heroPanel(width)
	stats := m.statsPanel(width)
	devices := m.devicesPanel(width)
	usage := m.usagePanel(width)
	heroH := lipgloss.Height(hero)
	statsH := lipgloss.Height(stats)
	devicesH := lipgloss.Height(devices)
	usageH := lipgloss.Height(usage)
	listH := height - heroH - statsH - devicesH - usageH
	if listH < 5 {
		listH = 5
	}
	sessions := m.topSessions(width, listH)
	return lipgloss.JoinVertical(lipgloss.Left, hero, stats, devices, usage, sessions)
}

func (m dashboardModel) heroPanel(width int) string {
	title := m.st.Title.Render("Hello.")
	sub := m.st.Subtitle.Render("Welcome to ccmux. One TUI for every agent session — Claude, Codex, Antigravity — every project, every device.")
	body := lipgloss.JoinVertical(lipgloss.Left, title, sub)
	return m.st.Pane.Width(width - 2).Render(body)
}

func (m dashboardModel) statsPanel(width int) string {
	active := 0
	idle := 0
	waiting := 0
	for _, s := range m.sessions {
		switch s.State {
		case "active":
			active++
		case "idle":
			idle++
		case "needs_input":
			waiting++
		}
	}
	rows := []string{
		m.st.Emphasis.Render("Session summary"),
		"",
		fmt.Sprintf("%s  %d active", m.st.StateActive.Render("●"), active),
		fmt.Sprintf("%s  %d idle", m.st.StateIdle.Render("●"), idle),
		fmt.Sprintf("%s  %d waiting for input", m.st.StateNeedsInput.Render("●"), waiting),
		"",
		m.st.Muted.Render(time.Now().Format("Mon Jan 2 — 15:04:05")),
	}
	return m.st.Pane.Width(width - 2).Render(strings.Join(rows, "\n"))
}

// devicesPanel renders the tailnet/network view. One row per device,
// two columns: [icon + name] | [info]. The info column is whichever
// single fact is most useful for that row:
//
//   - Reachable ccmuxd: version, with " · update available" if it
//     differs from this build's version.
//   - NeedsInstall:     "ccmuxd unreachable".
//   - Mobile:           "via Moshi app".
//
// Local row gets a muted "(this)" suffix on the name so the user can
// tell which row is which when multiple machines share the dashboard.
//
// Width-aware: the name column scales with the panel's inner width,
// capped at 28 chars so very wide terminals don't strand info too far
// right. Truncates names with an ellipsis rather than overflowing.
//
// Empty state hides the panel entirely so the dashboard stays tidy on
// single-machine setups.
func (m dashboardModel) devicesPanel(width int) string {
	if len(m.hosts) == 0 {
		return ""
	}
	st := m.st

	// Pane has a 1-col border on each side + 1-col padding = 4 chars
	// of chrome around the body. Stay safe on tiny terminals.
	inner := width - 4
	if inner < 30 {
		inner = 30
	}
	nameW := inner * 2 / 5
	if nameW > 28 {
		nameW = 28
	}
	if nameW < 12 {
		nameW = 12
	}
	rows := []string{st.Emphasis.Render("Devices")}
	for _, h := range m.hosts {
		icon := iconForHost(h, st)
		name := nameForHost(h, nameW)
		info := infoForHost(h, m.version, st)
		rows = append(rows, fmt.Sprintf("%s %s %s",
			icon, padRight(name, nameW), info))
	}
	if m.version != "" {
		rows = append(rows, "", st.Muted.Render("this build: "+m.version))
	}
	hasMissing := false
	for _, h := range m.hosts {
		if h.NeedsInstall {
			hasMissing = true
			break
		}
	}
	if hasMissing {
		rows = append(rows,
			st.Muted.Render("unreachable peer? install ccmux there with `make bootstrap`,"),
			st.Muted.Render("or run `ccmux setup` on it to enable server mode."))
	}
	return st.Pane.Width(width - 2).Render(strings.Join(rows, "\n"))
}

// iconForHost returns the colored status indicator for a row.
// Mobile peers get the 📱 glyph; everyone else gets a styled circle.
func iconForHost(h hostStatus, st styles.Styles) string {
	switch {
	case h.Mobile:
		return "📱"
	case h.NeedsInstall:
		return st.Muted.Render("○")
	case !h.OK:
		return st.StateError.Render("●")
	default:
		return st.StateActive.Render("●")
	}
}

// nameForHost is the row's primary label. For the local row we
// append a muted "(this)" marker so it's obvious which one is the
// current machine. Truncation respects nameW so the column stays
// aligned.
func nameForHost(h hostStatus, nameW int) string {
	if h.Local {
		const marker = " (this)"
		room := nameW - len(marker)
		if room < 4 {
			room = 4
		}
		return truncatePeerName(h.Name, room) + marker
	}
	return truncatePeerName(h.Name, nameW)
}

// infoForHost returns the right-hand column: one fact per row.
func infoForHost(h hostStatus, localVersion string, st styles.Styles) string {
	switch {
	case h.Mobile:
		return st.Muted.Render("via Moshi app")
	case h.NeedsInstall:
		return st.Muted.Render("ccmuxd unreachable")
	}
	ver := h.Version
	if ver == "" {
		ver = st.Muted.Render("?")
	}
	if localVersion != "" && h.Version != "" && versionsDiffer(localVersion, h.Version) {
		return ver + "  " + st.StatusWarning.Render("update available")
	}
	return ver
}

// truncatePeerName cuts a name to fit `n` visible columns, replacing
// the dropped tail with an ellipsis. Operates on runes so multi-byte
// characters (CJK, emoji) survive cleanly when they fit.
func truncatePeerName(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	if n <= 1 {
		runes := []rune(s)
		if len(runes) <= n {
			return s
		}
		return string(runes[:n])
	}
	runes := []rune(s)
	if len(runes) <= n-1 {
		return s
	}
	return string(runes[:n-1]) + "…"
}

// versionsDiffer normalizes the LDFLAGS-baked version strings (e.g.
// "1db9351", "1db9351-dirty", "v0.1.0") and reports whether they're
// the same commit. We treat "-dirty" suffixes as equivalent to the
// clean form so a developer's local working tree doesn't flag the
// peer as stale.
func versionsDiffer(local, remote string) bool {
	return normalizeVersion(local) != normalizeVersion(remote)
}

func normalizeVersion(v string) string {
	if i := strings.Index(v, "-dirty"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// usagePanel renders the Claude Code usage block: messages in the 5-hour
// subscription window, reset time, token totals, top projects, estimated
// $cost. Falls back gracefully when no data is available yet.
func (m dashboardModel) usagePanel(width int) string {
	st := m.st
	rows := []string{st.Emphasis.Render("Claude usage")}
	if m.usage == nil {
		rows = append(rows,
			"",
			st.Muted.Render("(loading transcripts…)"),
		)
		return st.Pane.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	a := m.usage
	rows = append(rows, "")

	// Subscription window summary. ResetAt is in UTC (timestamps in
	// Claude Code's transcripts are UTC); convert to the user's local
	// zone before formatting so "06:02" shows as "23:02" on the West
	// Coast, not in zulu time.
	//
	// Use UserPrompts (not Messages) for the headline count because
	// that's what Anthropic's per-window quota actually counts. The
	// Messages number lumps in every tool-result follow-up which would
	// inflate the visible total ~10-30x.
	headlineCount := a.UserPrompts
	headlineLabel := "prompts"
	if headlineCount == 0 {
		// Fall back gracefully if the JSONL didn't expose the user-
		// prompt shape we expect.
		headlineCount = a.Messages
		headlineLabel = "msgs"
	}
	msgChip := lipgloss.NewStyle().Foreground(st.P.Lavender).Bold(true).Render(
		fmt.Sprintf("%d %s", headlineCount, headlineLabel),
	)
	resetLine := ""
	if reset := a.ResetAt(5 * time.Hour); !reset.IsZero() {
		local := reset.Local()
		remaining := time.Until(reset)
		if remaining > 0 {
			resetLine = st.Muted.Render(fmt.Sprintf("resets %s (in %s)",
				local.Format("15:04"), humanDuration(remaining)))
		} else {
			resetLine = st.Muted.Render(fmt.Sprintf("resetting now (next: %s)",
				local.Format("15:04")))
		}
	}
	rows = append(rows, fmt.Sprintf("5h window  %s  %s", msgChip, resetLine))

	// Quota bar if a known subscription tier is configured. Always feed
	// the bar the user-prompt count rather than raw message count so it
	// approximates the same number Anthropic uses to enforce the limit.
	if bar := m.quotaBar(headlineCount, width-6); bar != "" {
		rows = append(rows, bar)
	}

	// Token breakdown — emphasize cache_read since that's where the
	// session-level efficiency shows up.
	rows = append(rows, "")
	rows = append(rows, fmt.Sprintf("tokens     %s in · %s out",
		st.Emphasis.Render(claudeusage.HumanCount(a.Total.Input)),
		st.Emphasis.Render(claudeusage.HumanCount(a.Total.Output)),
	))
	rows = append(rows, fmt.Sprintf("cache      %s create · %s read",
		st.Muted.Render(claudeusage.HumanCount(a.Total.CacheCreation)),
		st.StateActive.Render(claudeusage.HumanCount(a.Total.CacheRead)),
	))
	// API-rate cost estimate is informational only for subscription users.
	if cost := a.EstimatedCost(); cost > 0 {
		rows = append(rows, st.Muted.Render(
			fmt.Sprintf("~ $%.2f at API rates (subs = $0 beyond plan)", cost),
		))
	}

	// Top projects.
	if tp := a.TopProjects(3); len(tp) > 0 {
		rows = append(rows, "")
		rows = append(rows, st.Subtitle.Render("top projects this window"))
		for _, p := range tp {
			rows = append(rows, fmt.Sprintf("  %s   %s",
				p.Project,
				st.Muted.Render(claudeusage.HumanCount(p.Tokens.Total())),
			))
		}
	}

	// Per-agent rows for Codex and Antigravity. Each renders as a
	// single line — either "(no transcripts yet, install via …)" when
	// the walker is still a stub or returned no data, or "tokens · cost"
	// when there's real usage to show. The Claude block above is
	// already the rich panel; these two are deliberately compact so
	// the vertical budget on a narrow terminal stays usable.
	rows = append(rows, "")
	rows = append(rows, renderAgentUsageRow(st, "Codex", m.codexUsage,
		"`npm i -g @openai/codex`"))
	rows = append(rows, renderAgentUsageRow(st, "Antigravity", m.antigravityUsage,
		"`curl -fsSL https://antigravity.google/cli/install.sh | bash`"))

	return st.Pane.Width(width - 2).Render(strings.Join(rows, "\n"))
}

// renderAgentUsageRow formats one Codex / Antigravity line beneath the
// rich Claude panel. The shape:
//
//	Codex   no transcripts yet  (`npm i -g @openai/codex`)
//
// or when there's data:
//
//	Codex   123 prompts · 4.5K in · 12.1K out · ~$0.08
//
// Centralized so adding a fourth agent later is a one-line caller
// change rather than two-place markup duplication.
func renderAgentUsageRow(st styles.Styles, label string, s usage.AgentSummary, installHint string) string {
	prefix := st.Emphasis.Render(label) + "   "
	if !s.HasData {
		return prefix + st.Muted.Render("no transcripts yet  ("+installHint+")")
	}
	core := fmt.Sprintf("%d prompts · %s in · %s out",
		s.Prompts,
		claudeusage.HumanCount(s.InputTokens),
		claudeusage.HumanCount(s.OutputTokens),
	)
	if s.EstimatedCost > 0 {
		core += fmt.Sprintf(" · ~$%.2f", s.EstimatedCost)
	}
	return prefix + core
}

// quotaBar renders a 1-line progress bar when the user has declared a
// subscription tier in config. Empty string when tier is "api" or unset.
func (m dashboardModel) quotaBar(messages, width int) string {
	limit := planMessageLimit(m.cfg.Subscription.Tier)
	if limit <= 0 {
		return ""
	}
	ratio := float64(messages) / float64(limit)
	if ratio > 1 {
		ratio = 1
	}
	barW := width - 12
	if barW < 10 {
		barW = 10
	}
	filled := int(float64(barW) * ratio)
	if filled > barW {
		filled = barW
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barW-filled)

	color := m.st.StateActive
	switch {
	case ratio >= 0.9:
		color = m.st.StateError
	case ratio >= 0.7:
		color = m.st.StateNeedsInput
	}
	return fmt.Sprintf("%s  %s",
		color.Render(bar),
		m.st.Muted.Render(fmt.Sprintf("%d / %d", messages, limit)),
	)
}

// planMessageLimit returns Anthropic's documented soft cap on
// messages per 5-hour window for each subscription tier. The actual
// limits vary by traffic and model mix; these are sane defaults that
// can be overridden in future config.
func planMessageLimit(tier string) int {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "pro":
		return 45
	case "max5x", "max-5x", "max":
		return 225
	case "max20x", "max-20x":
		return 900
	}
	return 0 // api / unset → no quota bar
}

// topSessions produces a pane exactly `height` lines tall and `width` cells
// wide. We clamp the content to (height - 2) lines so Lipgloss's
// minimum-height semantics doesn't push the pane taller than requested.
func (m dashboardModel) topSessions(width, height int) string {
	if width < 16 {
		width = 16
	}
	if height < 5 {
		height = 5
	}
	// Pane border accounts for 2 lines; padding is 0 vertically.
	contentLines := height - 2

	header := m.st.Emphasis.Render("Sessions") + "  " + m.st.Muted.Render(fmt.Sprintf("(%d)", len(m.sessions)))
	rows := []string{header, ""}
	remaining := contentLines - len(rows)
	if remaining < 0 {
		remaining = 0
	}

	if len(m.sessions) == 0 {
		if remaining > 0 {
			rows = append(rows, m.st.Muted.Render("No active sessions."))
			remaining--
		}
		if remaining > 0 {
			rows = append(rows, "Press "+m.st.Key.Render("3")+" to start one.")
			remaining--
		}
	} else {
		inner := width - 4
		if inner < 10 {
			inner = 10
		}
		// If we have more sessions than rows, reserve one line for "and N more".
		maxSessions := remaining
		needsTail := len(m.sessions) > maxSessions
		if needsTail {
			maxSessions = remaining - 1
		}
		if maxSessions < 1 {
			maxSessions = 1
		}
		for i := 0; i < maxSessions && i < len(m.sessions); i++ {
			rows = append(rows, renderSessionLine(m.st, m.sessions[i], inner))
		}
		if needsTail {
			rows = append(rows, m.st.Muted.Render(fmt.Sprintf("… and %d more", len(m.sessions)-maxSessions)))
		}
	}

	// Pad to exactly contentLines so the pane renders at the target height.
	for len(rows) < contentLines {
		rows = append(rows, "")
	}
	if len(rows) > contentLines {
		rows = rows[:contentLines]
	}

	// Lipgloss Width/Height set CONTENT dimensions; border adds +2 to each.
	// To produce a pane exactly height x width cells, pass (width-2, height-2).
	return m.st.Pane.Width(width - 2).Height(contentLines).Render(strings.Join(rows, "\n"))
}

// renderSessionLine produces one line per session: host dot, state glyph,
// (optionally a "⊙" attached badge), name, age suffix. Attached sessions
// get a distinct mauve-bold-underlined name + an "attached" chip in the
// suffix so they're impossible to confuse with detached sessions even at
// a glance.
//
// `inner` is the content area available (already accounting for any
// surrounding pane border/padding).
func renderSessionLine(st styles.Styles, s daemon.SessionState, inner int) string {
	hostDot := st.HostColor(s.Host).Render("●")
	state := stateGlyph(st, s.State)

	attachedBadge := ""
	nameStyle := st.Emphasis
	if s.Attached {
		attachedBadge = lipgloss.NewStyle().Foreground(st.P.Mauve).Bold(true).Render("⊙ ")
		nameStyle = lipgloss.NewStyle().Foreground(st.P.Mauve).Bold(true).Underline(true)
	}

	age := ""
	if !s.LastChange.IsZero() {
		age = humanDuration(time.Since(s.LastChange))
	}

	// Build a small host tag so the user can tell at a glance which
	// device a session lives on. Skipped for local sessions and when
	// the row is narrow — the leading colored dot already encodes
	// host identity for tight layouts.
	hostTag := ""
	if h := s.Host; h != "" && h != "local" && inner > 50 {
		hostTag = "  " + st.Muted.Render("@"+h)
	}

	// Agent tag: only render when the session is NOT running the
	// default agent (claude). Showing "claude" on every row would just
	// be noise for users who haven't adopted Codex/Antigravity. Once a row
	// is on a non-default agent, the tag tells the user which one.
	agentTag := ""
	if s.Agent != "" && s.Agent != string(agent.IDClaude) && inner > 60 {
		agentTag = "  " + st.Muted.Render("["+s.Agent+"]")
	}

	var suffix string
	switch {
	case s.Attached && age != "":
		suffix = "  " + lipgloss.NewStyle().Foreground(st.P.Mauve).Bold(true).Render("attached") + " " + st.Muted.Render(age)
	case s.Attached:
		suffix = "  " + lipgloss.NewStyle().Foreground(st.P.Mauve).Bold(true).Render("attached")
	case age != "":
		suffix = "  " + st.Muted.Render(age)
	}
	suffix += hostTag
	suffix += agentTag

	prefix := hostDot + " " + state + " " + attachedBadge

	// Truncate name so the rendered line fits in `inner` cells. We use
	// lipgloss.Width on the styled fragments so ANSI doesn't fool us.
	nameBudget := inner - lipgloss.Width(prefix) - lipgloss.Width(suffix)
	if nameBudget < 6 {
		nameBudget = 6
	}
	name := s.Name
	if lipgloss.Width(name) > nameBudget {
		runes := []rune(name)
		if len(runes) > nameBudget-1 {
			runes = runes[:nameBudget-1]
		}
		name = string(runes) + "…"
	}
	return prefix + nameStyle.Render(name) + suffix
}

func stateGlyph(st styles.Styles, state string) string {
	switch state {
	case "active":
		return st.StateActive.Render("▶")
	case "idle":
		return st.StateIdle.Render("◌")
	case "needs_input":
		return st.StateNeedsInput.Render("!")
	case "error":
		return st.StateError.Render("✗")
	default:
		return st.StateUnknown.Render("?")
	}
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
