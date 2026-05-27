package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/claudeusage"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/selfupdate"
	"github.com/skzv/ccmux/internal/tui/components"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestRenderSessionLine_AgentTag_Default — local Claude sessions are
// the default. We deliberately do NOT show a `[claude]` tag on every
// row; that would just be visual noise for the 95% of users who
// haven't adopted a second agent. The tag only appears once the row
// is on a non-default agent.
func TestRenderSessionLine_AgentTag_Default(t *testing.T) {
	st := styles.Default()
	got := renderSessionLine(st, daemon.SessionState{
		Name:  "c-foo",
		Host:  "local",
		Agent: string(agent.IDClaude),
	}, 120)
	if strings.Contains(got, "[claude]") {
		t.Errorf("default-agent rows should not show a [claude] tag:\n%s", got)
	}
	if strings.Contains(got, "[codex]") || strings.Contains(got, "[antigravity]") {
		t.Errorf("non-default tags should not appear on claude row:\n%s", got)
	}
}

// TestRenderSessionLine_AgentTag_NonDefault — when the session is
// running Codex or Antigravity, the row gets a small `[codex]` /
// `[antigravity]` tag in muted styling so the user can tell at a
// glance which agent is driving which row.
func TestRenderSessionLine_AgentTag_NonDefault(t *testing.T) {
	st := styles.Default()
	for _, id := range []agent.ID{agent.IDCodex, agent.IDAntigravity} {
		t.Run(string(id), func(t *testing.T) {
			got := renderSessionLine(st, daemon.SessionState{
				Name:  "c-foo",
				Host:  "local",
				Agent: string(id),
			}, 120)
			want := "[" + string(id) + "]"
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in:\n%s", want, got)
			}
		})
	}
}

// TestRenderSessionLine_AgentTag_DroppedWhenNarrow — the tag is
// conditional on `inner > 60` so very narrow layouts (phone mode)
// don't get their session names truncated to fit the tag. Pin that
// threshold here so a future tweak doesn't silently break narrow
// rendering.
func TestRenderSessionLine_AgentTag_DroppedWhenNarrow(t *testing.T) {
	st := styles.Default()
	narrow := renderSessionLine(st, daemon.SessionState{
		Name:  "c-foo",
		Host:  "local",
		Agent: string(agent.IDCodex),
	}, 40)
	if strings.Contains(narrow, "[codex]") {
		t.Errorf("narrow row should not include agent tag:\n%s", narrow)
	}
}

// TestRenderSessionLine_AgentTag_EmptyMeansClaude — sessions whose
// project we couldn't resolve to a sidecar arrive with Agent="".
// Treat them as the default — no tag. This is the back-compat path
// for every session that existed before Phase 2.
func TestRenderSessionLine_AgentTag_EmptyMeansClaude(t *testing.T) {
	st := styles.Default()
	got := renderSessionLine(st, daemon.SessionState{
		Name:  "c-legacy",
		Host:  "local",
		Agent: "", // legacy
	}, 120)
	if strings.Contains(got, "[") {
		t.Errorf("empty Agent should render without a tag:\n%s", got)
	}
}

// TestDashboard_UpdateChip_ShownWhenBehind — once App pushes a
// positive update check, the status bar carries the chip
// (moved from the hero panel in the design-system redesign so the
// chrome doesn't compete with screen body for vertical space).
// Pluralization is pinned because "1 commit" vs "3 commits" is the
// kind of thing a refactor silently breaks.
func TestDashboard_UpdateChip_ShownWhenBehind(t *testing.T) {
	cases := []struct {
		behind int
		want   string
	}{
		{1, "1 commit"},
		{3, "3 commits"},
		{12, "12 commits"},
	}
	for _, tc := range cases {
		a := App{
			styles:       styles.Default(),
			keys:         DefaultKeymap(),
			width:        200,
			daemonOnline: true,
		}
		a.dashboard = newDashboard(a.styles, a.keys)
		a.dashboard.SetUpdateAvailable(selfupdate.Result{Behind: tc.behind, Branch: "main"})
		bar := a.renderStatusBar()
		if !strings.Contains(bar, "ccmux update") {
			t.Errorf("behind=%d: status bar missing the ccmux-update chip:\n%s", tc.behind, bar)
		}
		if !strings.Contains(bar, tc.want) {
			t.Errorf("behind=%d: chip should say %q:\n%s", tc.behind, tc.want, bar)
		}
	}
}

// TestDashboard_CcusageBlock_ShowsBurnRate — when a billing-block is
// pushed via SetCcusageBlock, the usage panel must surface cost and
// burn rate. Zero block (nil) must not crash and must not show the block.
func TestDashboard_CcusageBlock_ShowsBurnRate(t *testing.T) {
	st := styles.Default()
	m := newDashboard(st, DefaultKeymap())
	m.SetCcusageBlock(&ccusageBlock{
		CostUSD:             48.21,
		BurnRateCostPerHour: 25.1,
		ProjectedTotalCost:  125.22,
		IsActive:            true,
	})
	panel := m.usagePanel(120)
	for _, want := range []string{"$48.21", "$25.1/hr", "$125.22"} {
		if !strings.Contains(panel, want) {
			t.Errorf("ccusage block: panel missing %q:\n%s", want, panel)
		}
	}
}

// TestDashboard_CcusageBlock_NilDoesNotCrash — nil block means ccusage
// isn't installed; the panel must render the loading state without panic.
func TestDashboard_CcusageBlock_NilDoesNotCrash(t *testing.T) {
	m := newDashboard(styles.Default(), DefaultKeymap())
	// no SetCcusageBlock call — zero value / nil
	if got := m.usagePanel(120); got == "" {
		t.Error("usagePanel returned empty with nil ccusage block")
	}
}

// TestDashboard_UpdateChip_HiddenWhenUpToDate — the zero-value
// Result (no check, or check found 0 behind) must render NO chip.
// A chip that shows when there's no update would train users to
// ignore it.
func TestDashboard_UpdateChip_HiddenWhenUpToDate(t *testing.T) {
	a := App{
		styles:       styles.Default(),
		keys:         DefaultKeymap(),
		width:        200,
		daemonOnline: true,
	}
	a.dashboard = newDashboard(a.styles, a.keys)
	// No SetUpdateAvailable call — zero value.
	if strings.Contains(a.renderStatusBar(), "ccmux update") {
		t.Error("status bar shows the update chip with no update pending")
	}
	// Explicitly Behind=0 must also stay silent.
	a.dashboard.SetUpdateAvailable(selfupdate.Result{Behind: 0, Branch: "main"})
	if strings.Contains(a.renderStatusBar(), "ccmux update") {
		t.Error("Behind=0 should not render the chip")
	}
}

// TestDashboardPanels_NarrowOmitsT2 — below the breakpoint each panel
// drops its T2 (reference) content: the hero's welcome subtitle, the
// session-summary clock, the devices help text, and the usage panel
// collapses to a single headline line.
func TestDashboardPanels_NarrowOmitsT2(t *testing.T) {
	st := styles.Default()
	m := newDashboard(st, DefaultKeymap())
	m.narrow = true // homeView sets this from the terminal width
	m.version = "v1.2.3"
	m.SetHosts([]hostStatus{{Name: "peer", NeedsInstall: true}})
	m.SetCcusageBlock(&ccusageBlock{CostUSD: 48.21})
	m.SetUsage(&claudeusage.Aggregate{UserPrompts: 47})

	if strings.Contains(m.heroPanel(50), "Welcome to ccmux") {
		t.Error("narrow heroPanel still shows the welcome subtitle (T2)")
	}
	if dev := m.devicesPanel(50); strings.Contains(dev, "this build:") || strings.Contains(dev, "make bootstrap") {
		t.Errorf("narrow devicesPanel still shows T2 help:\n%s", dev)
	}
	u := m.usagePanel(50)
	if h := lipgloss.Height(u); h > 5 {
		t.Errorf("narrow usagePanel should collapse to ~one line, got %d rows:\n%s", h, u)
	}
	for _, gone := range []string{"Codex usage", "Antigravity", "cache", "top projects"} {
		if strings.Contains(u, gone) {
			t.Errorf("narrow usagePanel still shows T2 %q:\n%s", gone, u)
		}
	}
	if !strings.Contains(u, "47 prompts") {
		t.Errorf("narrow usagePanel lost the prompt count (T0):\n%s", u)
	}
}

// TestDashboardPanels_WideKeepsAgentSections — at wide widths the
// usage panel must surface per-agent sections for Codex and
// Antigravity, even when they have no data ("no conversations yet"
// keeps the visual cadence consistent across agents).
//
// The welcome subtitle that used to live in the hero is gone — the
// design-system redesign moved it to the first-launch tour so the
// dashboard reads in fewer rows. The hero is just "Hello." now.
func TestDashboardPanels_WideKeepsAgentSections(t *testing.T) {
	st := styles.Default()
	m := newDashboard(st, DefaultKeymap())
	m.SetCcusageBlock(&ccusageBlock{CostUSD: 48.21})
	m.SetUsage(&claudeusage.Aggregate{UserPrompts: 47})
	u := m.usagePanel(120)
	for _, want := range []string{"Codex · recent", "Antigravity · recent", "Claude · 5h window"} {
		if !strings.Contains(u, want) {
			t.Errorf("wide usagePanel missing %q:\n%s", want, u)
		}
	}
}

// TestDashboardGolden is the design-system visual regression net for
// the home (Dashboard) screen. It snapshots the screen body + the
// new components.HelpBar at the canonical 120x40 size and compares
// against testdata/golden/dashboard.txt.
//
// The tab strip and status bar are NOT in this snapshot — both depend
// on os.Hostname() / time.Now() in ways that would make the golden
// machine-dependent. They have their own deterministic unit tests
// (TestRenderHeader_*, TestStatusBar_*). This golden focuses on the
// per-screen surface, which is where the redesign's visual choices
// land.
//
// Determinism:
//   - dashboard.SetNow pins the live clock in statsPanel.
//   - Session LastChange / Created are set relative to time.Now()
//     so humanDuration's minute-resolution rounding produces the
//     same string on every run (within a 60-second test window).
//   - claudeusage and ccusage are left nil so the usage panel
//     renders its "(loading transcripts…)" placeholder rather than
//     time-varying token counts.
//
// To regenerate after an intentional visual change:
//
//	CCMUX_UPDATE_GOLDEN=1 go test ./internal/tui/ -run TestDashboardGolden
//
// Review the diff before committing — that's the design change you're
// shipping.
func TestDashboardGolden(t *testing.T) {
	a := buildDashboardGoldenApp()
	const width, height = 120, 40
	a.width = width
	a.height = height

	helpLine := forceSingleLine(components.HelpBar(a.styles, a.helpBarProps()), width)
	bodyH := height - lipgloss.Height(helpLine)
	if bodyH < 5 {
		bodyH = 5
	}
	body := a.homeView(width, bodyH)
	body = clampLines(body, bodyH)

	out := lipgloss.JoinVertical(lipgloss.Left, body, helpLine)
	goldenAssert(t, "dashboard.txt", out)
}

// buildDashboardGoldenApp constructs a deterministic App for golden
// rendering: fixed clock, fixed sessions, fixed hosts, fixed
// subscription tier. The intent is "what a user with two hosts and
// three sessions sees on a normal afternoon" — varied enough that
// the snapshot exercises every panel without random data noise.
func buildDashboardGoldenApp() App {
	const goldenVersion = "v0.0.0-golden"
	fixedClock := time.Date(2026, 5, 26, 14, 30, 0, 0, time.UTC)
	realNow := time.Now()

	cfg := config.Defaults()
	cfg.Subscription.Tier = "max5x"

	st := styles.Default()
	km := DefaultKeymap()

	a := App{
		cfg:          cfg,
		styles:       st,
		keys:         km,
		version:      goldenVersion,
		screen:       ScreenSessions,
		daemonOnline: true,
		lastRefresh:  fixedClock,
	}

	a.dashboard = newDashboard(st, km)
	a.dashboard.SetConfig(cfg)
	a.dashboard.SetVersion(goldenVersion)
	a.dashboard.SetNow(fixedClock)

	a.sessionsM = newSessions(st, km)

	sessions := []daemon.SessionState{
		{
			Name:       "ccmux-redesign",
			Host:       "local",
			State:      "active",
			Project:    "ccmux",
			Path:       "/Users/me/repos/ccmux",
			Attached:   true,
			Windows:    3,
			Agent:      "claude",
			Created:    realNow.Add(-2 * time.Hour),
			LastChange: realNow.Add(-3 * time.Minute),
		},
		{
			Name:       "ccmux-debug",
			Host:       "local",
			State:      "idle",
			Project:    "ccmux",
			Path:       "/Users/me/repos/ccmux",
			Windows:    1,
			Agent:      "claude",
			Created:    realNow.Add(-3 * time.Hour),
			LastChange: realNow.Add(-25 * time.Minute),
		},
		{
			Name:       "infra-watcher",
			Host:       "atelier",
			State:      "needs_input",
			Project:    "infra",
			Windows:    2,
			Agent:      "codex",
			Created:    realNow.Add(-1 * time.Hour),
			LastChange: realNow.Add(-5 * time.Minute),
		},
	}
	a.sessions = sessions
	a.sessionsM.SetSessions(sessions)
	a.dashboard.SetSessions(sessions)

	hosts := []hostStatus{
		{Name: "sputnik", Local: true, OK: true, Version: goldenVersion},
		{Name: "atelier", OK: true, Version: goldenVersion},
	}
	a.hosts = hosts
	a.dashboard.SetHosts(hosts)
	a.sessionsM.SetHosts(hosts)

	// Usage fixture — gives the new Usage panel actual numbers to
	// render instead of the "(loading transcripts…)" placeholder, so
	// the golden captures the real sub-section layout.
	a.dashboard.SetUsage(&claudeusage.Aggregate{
		UserPrompts: 47,
		Messages:    47,
		Total: claudeusage.Tokens{
			Input:         1_200_000,
			Output:        380_000,
			CacheRead:     2_100_000,
			CacheCreation: 340_000,
		},
	})
	// EndTime is anchored to a fixed wall-clock hour on today's date
	// so the "projected $X by HH:MM" line formats to the same minute
	// across runs. realNow varies; pinning the time-of-day stabilises
	// the golden regardless of when in the day the test runs.
	endTime := time.Date(realNow.Year(), realNow.Month(), realNow.Day(), 19, 30, 0, 0, time.Local)
	a.dashboard.SetCcusageBlock(&ccusageBlock{
		CostUSD:             48.21,
		BurnRateCostPerHour: 9.2,
		ProjectedTotalCost:  73.18,
		IsActive:            true,
		EndTime:             endTime,
	})

	return a
}
