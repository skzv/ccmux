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
	"github.com/skzv/ccmux/internal/selfupdate"
	"github.com/skzv/ccmux/internal/tui/components"
	"github.com/skzv/ccmux/internal/tui/styles"
	"github.com/skzv/ccmux/internal/usage"
)

// dashboardModel renders the at-a-glance landing screen.
// On wide terminals: hero + (sessions list | stats + usage).
// On narrow terminals (< 120 cols): everything stacked vertically.
type dashboardModel struct {
	st       styles.Styles
	km       Keymap
	sessions []daemon.SessionState
	hosts    []hostStatus
	version  string // this build's ccmux version, for the device-network panel
	cfg      config.Config
	usage    *claudeusage.Aggregate
	usageAt  time.Time

	// narrow mirrors the terminal's layout state (isNarrow of the
	// terminal width). homeView sets it before rendering so the panels
	// curate by terminal width, not by their own column width — a
	// half-width column on a monitor is itself < 120 yet must still
	// show the full (wide) content.
	narrow bool

	// Cross-agent token-usage summaries pushed by App on every
	// usageLoadedMsg. Codex/Antigravity are zero-valued today (stub
	// walkers; see internal/usage); the renderer keys off HasData
	// to decide between "real numbers" and "install hint".
	codexUsage       usage.AgentSummary
	antigravityUsage usage.AgentSummary

	// updateAvailable is set by App when the launch-time auto-update
	// check finds the local checkout behind upstream. Zero value
	// (Behind=0) renders no banner.
	updateAvailable selfupdate.Result

	// ccusage holds the most recent billing-block data from
	// `npx ccusage blocks --json`. Nil means ccusage isn't installed or
	// the last call failed — the usage panel silently skips the block
	// summary when nil.
	ccusage *ccusageBlock

	// now is a deterministic clock injection used by golden tests so
	// the wall-clock render in statsPanel doesn't vary by run. Zero
	// value falls back to time.Now() — production code never sets it.
	now time.Time
}

// SetNow injects a deterministic clock so the dashboard renders the
// same output across runs. Tests set this before snapshotting; the
// production launch path leaves it zero and the dashboard falls back
// to time.Now().
func (m *dashboardModel) SetNow(t time.Time) { m.now = t }

// clock returns the dashboard's current time — the injected value if
// set, else time.Now(). Internal helper for time-dependent renders.
func (m dashboardModel) clock() time.Time {
	if m.now.IsZero() {
		return time.Now()
	}
	return m.now
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

// SetCcusageBlock receives billing-block data from `npx ccusage blocks`.
// Nil clears any previous value (e.g., when ccusage becomes unreachable).
func (m *dashboardModel) SetCcusageBlock(b *ccusageBlock) { m.ccusage = b }

// SetUpdateAvailable records a positive auto-update check so the
// dashboard renders the "update available" banner. Called by App
// only when the check succeeded AND found commits behind.
func (m *dashboardModel) SetUpdateAvailable(r selfupdate.Result) {
	m.updateAvailable = r
}

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

// HelpBarProps returns the screen-level HelpBar contract for the
// home screen — the union of global keys (?, q, screen numbers)
// and the home-screen actions (attach, new, kill, rename, refresh,
// u for the usage-detail overlay).
// Priorities order the collapse: ? and q survive at any width.
func (m dashboardModel) HelpBarProps(width int) components.HelpBarProps {
	return components.HelpBarProps{
		Hints: []components.KeyHint{
			{Key: "?", Label: "help", Priority: 10},
			{Key: "q", Label: "quit", Priority: 10},
			{Key: "enter", Label: "attach", Priority: 8},
			{Key: "n", Label: "new", Priority: 7},
			{Key: "x", Label: "kill", Priority: 6},
			{Key: "u", Label: "usage", Priority: 5},
			{Key: "R", Label: "rename", Priority: 4},
			{Key: "r", Label: "refresh", Priority: 3},
			{Key: "1-7", Label: "screens", Priority: 2},
		},
		Width: width,
	}
}

// StatsView renders the dashboard's stat tiles — devices and usage
// — stacked in a single column. The legacy session-summary tile
// dissolved into the Sessions pane title (sessionsModel.sessionsCount
// produces the `(3 · 1 active · 1 idle · 1 waiting)` inline count
// breakdown) so the tile no longer earns its vertical real estate.
// The hero panel and the sessions list are rendered separately by
// homeView() in app.go.
func (m dashboardModel) StatsView(width int) string {
	devices := m.devicesPanel(width)
	usage := m.usagePanel(width)
	return lipgloss.JoinVertical(lipgloss.Left, devices, usage)
}

// heroPanel renders the minimal screen-top greeting. The previous
// welcome subtitle ("Welcome to ccmux. One TUI for every agent
// session …") was screen-redesign-checkpoint feedback as too much
// chrome for repeat opens, so it's gone — first-launch users see it
// in the tour instead. The inline update banner also moved out of
// the hero into the status bar so it doesn't compete with screen
// content for vertical real estate (see renderStatusBar).
func (m dashboardModel) heroPanel(width int) string {
	_ = width
	return "   " + m.st.Title.Render("Hello.")
}

// devicesPanel renders the tailnet/network view. Two layouts:
//
//   - One-line strip when there are 3 or fewer devices and they fit
//     the pane's inner width: "Devices  ● sputnik (this)  ● atelier".
//   - Multi-row list otherwise (4+ devices, or strip overflowed):
//     each device on its own row with the same row renderer.
//
// Per-device chips signal action: "[↑ update]" when a remote peer's
// ccmuxd version differs from this build, "(unreachable)" muted when
// the peer's ccmuxd isn't responding. The local row never carries an
// update chip. Reference detail (this build version, install help)
// moved out — the status bar carries the local-update chip and the
// `?` help overlay carries the install instructions.
func (m dashboardModel) devicesPanel(width int) string {
	if len(m.hosts) == 0 {
		return ""
	}
	st := m.st
	inner := width - 4

	// Adaptive layout: try a one-line strip when the device count is
	// small AND the rendered strip fits the inner width.
	if len(m.hosts) <= 3 {
		strip := m.renderDevicesStrip()
		heading := st.Emphasis.Render("Devices")
		oneLine := heading + "   " + strip
		if lipgloss.Width(oneLine) <= inner {
			return st.Pane.Width(width - 2).MaxWidth(width).Render(oneLine)
		}
	}

	// Multi-row list. Header + blank + one row per device.
	rows := []string{st.Emphasis.Render("Devices"), ""}
	for _, h := range m.hosts {
		rows = append(rows, "  "+m.renderDeviceRow(h))
	}
	return st.Pane.Width(width - 2).MaxWidth(width).Render(strings.Join(rows, "\n"))
}

// renderDevicesStrip joins each device row with a small gap. Used
// only by the one-line layout in devicesPanel.
func (m dashboardModel) renderDevicesStrip() string {
	parts := make([]string, 0, len(m.hosts))
	for _, h := range m.hosts {
		parts = append(parts, m.renderDeviceRow(h))
	}
	return strings.Join(parts, "   ")
}

// renderDeviceRow renders a single device entry: status dot + name
// (host-colored) + optional "(this)" suffix on the local row +
// optional "[↑ update]" chip when the peer's ccmuxd is on a different
// build than the local ccmux.
func (m dashboardModel) renderDeviceRow(h hostStatus) string {
	st := m.st
	var dot string
	switch {
	case h.Mobile:
		dot = "📱"
	case h.NeedsInstall:
		dot = st.Muted.Render("○")
	case !h.OK:
		dot = st.StateError.Render("●")
	default:
		dot = st.HostColor(h.Name).Render("●")
	}
	name := st.HostColor(h.Name).Render(h.Name)
	if h.Local {
		name += " " + st.Muted.Render("(this)")
	}
	row := dot + " " + name
	switch {
	case h.Mobile:
		row += " " + st.Muted.Render("(Moshi)")
	case h.NeedsInstall:
		row += " " + st.Muted.Render("(unreachable)")
	case !h.Local && h.Version != "" && m.version != "" && versionsDiffer(m.version, h.Version):
		row += " " + lipgloss.NewStyle().Foreground(st.P.Yellow).Bold(true).Render("[↑ update]")
	}
	return row
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

// usagePanel renders the redesigned consolidated usage block.
//
// Layout (wide):
//
//	Usage
//
//	  Claude · 5h window
//	    47 / 225 (est.) prompts  ·  21% used
//	    █████░░░░░░░░░░░░░░░░░░░░
//	    resets in 4h 57m
//
//	  Cost · billing block
//	    $48.21 spent  ·  $9.20/hr
//	    projected $73 by 22:30
//
//	  Tokens · this window
//	    1.2M in  ·  380K out  ·  2.1M cache hit
//
//	  Codex · recent
//	    no conversations yet              (or live numbers when HasData)
//
//	  Antigravity · recent
//	    no conversations yet
//
//	  press u for top projects · costs · per-agent
//
// Section headings are agent-colored so the eye learns which agent
// is which (Claude=mauve, Codex=sky, Antigravity=peach). Headline
// numbers are lavender bold; supporting text is muted; cache hit is
// green. The Anthropic per-window limit is rendered with "(est.)"
// because Anthropic does not publish exact per-tier caps — we use
// the soft defaults from planMessageLimit. On narrow terminals the
// whole panel collapses to a single line via usageSummaryLine.
func (m dashboardModel) usagePanel(width int) string {
	st := m.st
	if m.narrow {
		return st.Pane.Width(width - 2).MaxWidth(width).Render(m.usageSummaryLine())
	}
	if m.usage == nil && m.ccusage == nil {
		return st.Pane.Width(width - 2).MaxWidth(width).Render(strings.Join([]string{
			st.Emphasis.Render("Usage"),
			"",
			st.Muted.Render("(loading transcripts…)"),
		}, "\n"))
	}

	rows := []string{st.Emphasis.Render("Usage"), ""}

	// Indent hierarchy is 4 levels:
	//
	//   Usage                            (0 — panel title)
	//    Claude · 5h window              (1 — agent heading)
	//      47 / 225 (est.) prompts...    (3 — agent body)
	//      Cost · billing block          (3 — Claude sub-section heading)
	//        $48.21 spent ...            (5 — Claude sub-section body)
	//      Tokens · this window          (3 — Claude sub-section heading)
	//        1.2M in · 380K out ...      (5 — Claude sub-section body)
	//    Codex · recent                  (1 — peer agent heading)
	//      no conversations yet          (3 — peer agent body)
	//
	// Cost and Tokens nest UNDER Claude because both are Claude-
	// specific data (ccusage scrapes Anthropic's billing block,
	// claudeusage walks Claude's transcripts). Codex and Antigravity
	// don't have an equivalent so they stay as flat peer sections.
	if m.usage != nil {
		rows = append(rows, " "+agentSectionHeading(st, agent.IDClaude, "Claude · 5h window"))
		rows = append(rows, m.renderClaudeWindowSection()...)
		if m.ccusage != nil {
			rows = append(rows, "")
			rows = append(rows, "   "+st.Subtitle.Render("Cost · billing block"))
			rows = append(rows, m.renderCostSection()...)
		}
		rows = append(rows, "")
		rows = append(rows, "   "+st.Subtitle.Render("Tokens · this window"))
		rows = append(rows, m.renderTokensSection()...)
		rows = append(rows, "")
	} else if m.ccusage != nil {
		// Edge case: ccusage block arrived before transcripts. Render
		// Cost as its own top-level section (no Claude heading to
		// nest under).
		rows = append(rows, " "+st.Subtitle.Render("Cost · billing block"))
		rows = append(rows, m.renderCostSection()...)
		rows = append(rows, "")
	}

	rows = append(rows, " "+agentSectionHeading(st, agent.IDCodex, "Codex · recent"))
	rows = append(rows, m.renderOtherAgentSection(m.codexUsage)...)
	rows = append(rows, "")
	rows = append(rows, " "+agentSectionHeading(st, agent.IDAntigravity, "Antigravity · recent"))
	rows = append(rows, m.renderOtherAgentSection(m.antigravityUsage)...)
	rows = append(rows, "")
	rows = append(rows, " "+st.Muted.Render("press ")+st.Key.Render("u")+
		st.Muted.Render(" for top projects · cache hit rate · per-prompt cost"))

	return st.Pane.Width(width - 2).MaxWidth(width).Render(strings.Join(rows, "\n"))
}

// agentSectionHeading renders the per-agent sub-section title in
// the agent's accent color. The colour mapping lives on
// styles.Styles.AgentAccent as the design-system single source of
// truth (Claude=mauve, Codex=sky, Antigravity=peach, Cursor=teal);
// the dashboard adds Bold for the heading treatment.
func agentSectionHeading(st styles.Styles, id agent.ID, text string) string {
	return st.AgentAccent(id).Bold(true).Render(text)
}

// renderClaudeWindowSection produces the indented body rows of the
// "Claude · 5h window" section: headline + bar + reset.
func (m dashboardModel) renderClaudeWindowSection() []string {
	st := m.st
	a := m.usage

	headlineCount := a.UserPrompts
	headlineLabel := "prompts"
	if headlineCount == 0 {
		headlineCount = a.Messages
		headlineLabel = "msgs"
	}
	limit := planMessageLimit(m.cfg.Subscription.Tier)

	count := lipgloss.NewStyle().Foreground(st.P.Lavender).Bold(true).
		Render(fmt.Sprintf("%d", headlineCount))
	headline := "   " + count
	if limit > 0 {
		headline += " " + st.Muted.Render(fmt.Sprintf("/ %d (est.) %s", limit, headlineLabel))
		ratio := float64(headlineCount) / float64(limit)
		if ratio > 1 {
			ratio = 1
		}
		pct := int(ratio * 100)
		pctStyle := lipgloss.NewStyle().Foreground(st.P.Green).Bold(true)
		switch {
		case ratio >= 0.9:
			pctStyle = lipgloss.NewStyle().Foreground(st.P.Red).Bold(true)
		case ratio >= 0.7:
			pctStyle = lipgloss.NewStyle().Foreground(st.P.Yellow).Bold(true)
		}
		headline += st.Muted.Render("  ·  ") + pctStyle.Render(fmt.Sprintf("%d%% used", pct))
	} else {
		headline += " " + st.Muted.Render(headlineLabel)
	}

	lines := []string{headline}
	if bar := m.renderUsageBar(headlineCount, limit, 25); bar != "" {
		lines = append(lines, "   "+bar)
	}
	if reset := a.ResetAt(5 * time.Hour); !reset.IsZero() {
		if remaining := time.Until(reset); remaining > 0 {
			lines = append(lines, "   "+st.Muted.Render("resets in "+humanDuration(remaining)))
		} else {
			lines = append(lines, "   "+st.Muted.Render("resetting now"))
		}
	}
	return lines
}

// renderUsageBar produces a fixed-width ASCII progress bar colored
// by ratio (green / yellow / red). Empty string when there's no
// limit configured for this tier.
func (m dashboardModel) renderUsageBar(count, limit, width int) string {
	if limit <= 0 || width <= 0 {
		return ""
	}
	ratio := float64(count) / float64(limit)
	if ratio > 1 {
		ratio = 1
	}
	filled := int(float64(width) * ratio)
	if filled > width {
		filled = width
	}
	color := m.st.P.Green
	switch {
	case ratio >= 0.9:
		color = m.st.P.Red
	case ratio >= 0.7:
		color = m.st.P.Yellow
	}
	return lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled)) +
		m.st.Muted.Render(strings.Repeat("░", width-filled))
}

// renderCostSection produces the body rows for the billing-block
// section: spent · burn rate, optionally projected total.
//
// Format choices: spent and projected use %.2f (cents matter — that's
// what the user is comparing across sessions). Burn rate uses %.1f
// because hourly rates aren't precise to the cent anyway and one
// decimal reads cleaner.
func (m dashboardModel) renderCostSection() []string {
	st := m.st
	b := m.ccusage
	spent := lipgloss.NewStyle().Foreground(st.P.Lavender).Bold(true).Render(fmt.Sprintf("$%.2f", b.CostUSD))
	line := "     " + spent + " " + st.Muted.Render("spent")
	if b.BurnRateCostPerHour > 0 {
		rate := lipgloss.NewStyle().Foreground(st.P.Lavender).Bold(true).
			Render(fmt.Sprintf("$%.1f", b.BurnRateCostPerHour))
		line += st.Muted.Render("  ·  ") + rate + st.Muted.Render("/hr")
	}
	lines := []string{line}
	if b.ProjectedTotalCost > 0 && b.IsActive {
		projection := lipgloss.NewStyle().Foreground(st.P.Peach).Bold(true).
			Render(fmt.Sprintf("$%.2f", b.ProjectedTotalCost))
		local := b.EndTime.Local()
		lines = append(lines, "     "+st.Muted.Render("projected ")+projection+
			st.Muted.Render(" by "+local.Format("15:04")))
	}
	return lines
}

// renderTokensSection produces the one-line tokens summary:
// input · output · cache (cache highlighted green when nonzero).
func (m dashboardModel) renderTokensSection() []string {
	st := m.st
	a := m.usage
	in := lipgloss.NewStyle().Foreground(st.P.Lavender).Bold(true).Render(claudeusage.HumanCount(a.Total.Input))
	out := lipgloss.NewStyle().Foreground(st.P.Lavender).Bold(true).Render(claudeusage.HumanCount(a.Total.Output))
	line := "     " + in + st.Muted.Render(" in  ·  ") + out + st.Muted.Render(" out")
	if a.Total.CacheRead > 0 {
		cache := lipgloss.NewStyle().Foreground(st.P.Green).Bold(true).
			Render(claudeusage.HumanCount(a.Total.CacheRead))
		line += st.Muted.Render("  ·  ") + cache + st.Muted.Render(" cache hit")
	}
	return []string{line}
}

// renderUsageOverlay produces the full-detail usage modal opened by
// pressing `u` on the home screen. It surfaces the data the inline
// Usage panel deferred — top projects in the 5-hour window, cache
// efficiency, cost-per-prompt, and explicit notes about which
// figures are estimates vs. measured. The dashboard's inline panel
// is the "at a glance"; this overlay is the "deep dive."
func (m dashboardModel) renderUsageOverlay(st styles.Styles, width, height int) string {
	lines := []string{
		st.Emphasis.Render("Usage detail"),
		st.Subtitle.Render("Pressed-u expansion of the dashboard's Usage panel — top projects, cache efficiency, cost-per-prompt, and the data gaps the inline panel papers over."),
		"",
	}

	if m.usage == nil && m.ccusage == nil {
		lines = append(lines, st.Muted.Render("(loading transcripts…)"))
	}

	// Subscription tier + the (est.) caveat.
	if tier := m.cfg.Subscription.Tier; tier != "" {
		limit := planMessageLimit(tier)
		lines = append(lines, st.Subtitle.Render("Subscription"))
		lines = append(lines, fmt.Sprintf("  tier            %s", tier))
		if limit > 0 {
			lines = append(lines,
				fmt.Sprintf("  per-window cap  %d prompts (est.)", limit),
				st.Muted.Render("                  Anthropic does not publish exact caps;"),
				st.Muted.Render("                  ccmux uses a soft default per tier."),
			)
		}
		lines = append(lines, "")
	}

	// Claude detail: prompts, tokens, cache, cost-per-prompt.
	if a := m.usage; a != nil {
		lines = append(lines, st.Subtitle.Render("Claude · 5h window"))
		lines = append(lines, fmt.Sprintf("  prompts         %d (user) · %d (total messages)",
			a.UserPrompts, a.Messages))
		lines = append(lines, fmt.Sprintf("  input tokens    %s",
			claudeusage.HumanCount(a.Total.Input)))
		lines = append(lines, fmt.Sprintf("  output tokens   %s",
			claudeusage.HumanCount(a.Total.Output)))
		lines = append(lines, fmt.Sprintf("  cache create    %s",
			claudeusage.HumanCount(a.Total.CacheCreation)))
		lines = append(lines, fmt.Sprintf("  cache read      %s",
			claudeusage.HumanCount(a.Total.CacheRead)))
		// Cache efficiency: how much of the input came from cache reads.
		// 0..1 ratio. Useful signal of whether prompt caching is working.
		if a.Total.Input > 0 {
			total := a.Total.Input + a.Total.CacheRead + a.Total.CacheCreation
			if total > 0 {
				ratio := float64(a.Total.CacheRead) / float64(total)
				lines = append(lines, fmt.Sprintf("  cache hit rate  %.0f%%", ratio*100))
			}
		}
		if cost := a.EstimatedCost(); cost > 0 {
			lines = append(lines, fmt.Sprintf("  est. cost       $%.2f at API rates", cost))
			if a.UserPrompts > 0 {
				lines = append(lines, fmt.Sprintf("  per-prompt avg  $%.4f",
					cost/float64(a.UserPrompts)))
			}
			lines = append(lines, st.Muted.Render("                  subscription users pay $0 in-plan;"))
			lines = append(lines, st.Muted.Render("                  the figure is what the same usage"))
			lines = append(lines, st.Muted.Render("                  would cost on the pay-per-token API."))
		}
		lines = append(lines, "")

		// Top projects.
		if tp := a.TopProjects(5); len(tp) > 0 {
			lines = append(lines, st.Subtitle.Render("Top projects · this 5h window"))
			for _, p := range tp {
				lines = append(lines, fmt.Sprintf("  %-32s %s",
					truncate(p.Project, 32),
					st.Muted.Render(claudeusage.HumanCount(p.Tokens.Total())+" tokens"),
				))
			}
			lines = append(lines, "")
		}
	}

	// Billing-block (ccusage) detail.
	if b := m.ccusage; b != nil {
		lines = append(lines, st.Subtitle.Render("Cost · billing block (via ccusage)"))
		lines = append(lines, fmt.Sprintf("  spent           $%.2f", b.CostUSD))
		if b.BurnRateCostPerHour > 0 {
			lines = append(lines, fmt.Sprintf("  burn rate       $%.2f/hr", b.BurnRateCostPerHour))
		}
		if b.ProjectedTotalCost > 0 && b.IsActive {
			local := b.EndTime.Local()
			lines = append(lines, fmt.Sprintf("  projected       $%.2f by %s",
				b.ProjectedTotalCost, local.Format("15:04")))
		}
		lines = append(lines, "")
	}

	// Per-agent.
	lines = append(lines, st.Subtitle.Render("Other agents"))
	for _, ag := range []struct {
		id   agent.ID
		name string
		s    usage.AgentSummary
	}{
		{agent.IDCodex, "Codex", m.codexUsage},
		{agent.IDAntigravity, "Antigravity", m.antigravityUsage},
	} {
		heading := agentSectionHeading(st, ag.id, "  "+ag.name)
		if !ag.s.HasData {
			lines = append(lines, heading+"  "+st.Muted.Render("— no conversations yet"))
			continue
		}
		parts := []string{fmt.Sprintf("%d prompts", ag.s.Prompts)}
		if ag.s.InputTokens > 0 || ag.s.OutputTokens > 0 {
			parts = append(parts, fmt.Sprintf("%s in · %s out",
				claudeusage.HumanCount(ag.s.InputTokens),
				claudeusage.HumanCount(ag.s.OutputTokens),
			))
		} else {
			parts = append(parts, "tokens unavailable (opaque transcript format)")
		}
		if ag.s.EstimatedCost > 0 {
			parts = append(parts, fmt.Sprintf("~$%.2f est.", ag.s.EstimatedCost))
		} else {
			parts = append(parts, "no cost estimate (no ccusage-equivalent for this agent)")
		}
		lines = append(lines, heading+"  "+st.Muted.Render(strings.Join(parts, "  ·  ")))
	}
	lines = append(lines, "")

	lines = append(lines, st.Muted.Render("press u or esc to close"))

	modalW := minInt(96, width-4)
	body := strings.Join(lines, "\n")
	modal := st.PaneFocused.Width(modalW).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

// renderOtherAgentSection produces the body rows for a non-Claude
// agent (Codex / Antigravity). When the agent has no data yet, the
// section renders a single muted "no conversations yet" — install
// hints are deliberately omitted.
//
// When the agent has data, the section is explicit about what we can
// and can't compute. Non-Claude agents don't have a ccusage equivalent
// scraping Anthropic-style billing blocks, so cost is rarely populated;
// when it isn't, the row says "no cost estimate" rather than silently
// omitting (asymmetry users notice on a side-by-side comparison).
// Antigravity's protobuf transcripts are opaque, so when prompts are
// counted but tokens aren't, the row says "tokens unavailable" for
// the same reason.
func (m dashboardModel) renderOtherAgentSection(s usage.AgentSummary) []string {
	st := m.st
	if !s.HasData {
		return []string{"   " + st.Muted.Render("no conversations yet")}
	}
	prompts := lipgloss.NewStyle().Foreground(st.P.Lavender).Bold(true).
		Render(fmt.Sprintf("%d", s.Prompts))
	line := "   " + prompts + " " + st.Muted.Render("prompts")
	if s.InputTokens > 0 || s.OutputTokens > 0 {
		in := lipgloss.NewStyle().Foreground(st.P.Lavender).Bold(true).
			Render(claudeusage.HumanCount(s.InputTokens))
		out := lipgloss.NewStyle().Foreground(st.P.Lavender).Bold(true).
			Render(claudeusage.HumanCount(s.OutputTokens))
		line += st.Muted.Render("  ·  ") + in + st.Muted.Render(" in  ·  ") +
			out + st.Muted.Render(" out")
	} else {
		line += st.Muted.Render("  ·  tokens unavailable")
	}
	if s.EstimatedCost > 0 {
		cost := lipgloss.NewStyle().Foreground(st.P.Lavender).Bold(true).
			Render(fmt.Sprintf("$%.2f", s.EstimatedCost))
		line += st.Muted.Render("  ·  ~") + cost + st.Muted.Render(" est.")
	} else {
		line += st.Muted.Render("  ·  no cost estimate")
	}
	return []string{line}
}

// usageSummaryLine is the one-line collapse of usagePanel for narrow
// terminals: the Claude headline only — prompt count, block cost, and
// reset time. Everything the wide panel adds (cache, tokens, top
// projects, the Codex/Antigravity blocks) is T2 and dropped here.
// Panel title is "Usage" (matching the wide layout's panel heading)
// so the narrow and wide phrasings stay consistent.
func (m dashboardModel) usageSummaryLine() string {
	st := m.st
	parts := []string{st.Emphasis.Render("Usage")}
	if a := m.usage; a != nil {
		count, label := a.UserPrompts, "prompts"
		if count == 0 {
			count, label = a.Messages, "msgs"
		}
		parts = append(parts, fmt.Sprintf("%d %s", count, label))
	} else {
		parts = append(parts, st.Muted.Render("loading…"))
	}
	if b := m.ccusage; b != nil {
		parts = append(parts, fmt.Sprintf("$%.2f", b.CostUSD))
	}
	if a := m.usage; a != nil {
		if reset := a.ResetAt(5 * time.Hour); !reset.IsZero() {
			parts = append(parts, "resets "+reset.Local().Format("15:04"))
		}
	}
	return strings.Join(parts, " · ")
}

// renderAgentUsageBlock formats a Claude-shaped rich block for the
// non-Claude agents. Headline → activity row → cost row → install
// hint (only when we have nothing else to say). Mirrors the
// vocabulary of the Claude block immediately above so the three
// agents read as peers without per-agent special-casing in the
// dashboard view.
//
// Why not parameterize the Claude block itself: Claude carries
// subscription-window semantics (5h quota bar, ResetAt) that the
// other two don't have. A "unify into one renderer" pass would
// either drop those Claude-specific bits or paper over them with
// nil-checks. Easier to keep two functions and audit the markup
// drift via TestRenderAgentUsageBlock_ShapeMatchesClaude (no such
// test yet — covered structurally by the existing shape tests).
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
