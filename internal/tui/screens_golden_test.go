package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/notes"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/components"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// composeScreen layers a screen's body output with its HelpBar in the
// same arrangement the running App composes (minus the tab strip and
// status bar, which depend on os.Hostname() / time.Now() and would
// make goldens machine-dependent). Used by every per-screen golden so
// they exercise the same chrome flow.
func composeScreen(body, helpLine string, height int) string {
	bodyH := height - lipgloss.Height(helpLine)
	if bodyH < 5 {
		bodyH = 5
	}
	body = clampLines(body, bodyH)
	body = padToHeight(body, bodyH)
	return lipgloss.JoinVertical(lipgloss.Left, body, helpLine)
}

// renderHelpBarFor is a thin wrapper that mirrors app.renderHelpLine
// for test composition.
func renderHelpBarFor(st styles.Styles, props components.HelpBarProps, width int) string {
	return forceSingleLine(components.HelpBar(st, props), width)
}

// TestConversationsGolden snapshots the Conversations screen at the
// just-below-breakpoint width 119 — the narrow layout that drops the
// detail pane. We snapshot narrow because the wide detail pane shows
// `c.LastActivity.Format("2006-01-02 15:04")`, an absolute timestamp
// that drifts every minute and makes the golden flake.
//
// fakeConversations() supplies relative offsets (now, now-24h,
// now-7d) so the list rendering's `relativeTimeShort` rounds to
// stable buckets ("now", "1d", "7d") within a one-minute test
// window.
func TestConversationsGolden(t *testing.T) {
	const width, height = 119, 40
	st := styles.Default()
	km := DefaultKeymap()

	m := newConversations(st, km)
	m.SetList(fakeConversations())

	helpLine := renderHelpBarFor(st, m.HelpBarProps(width), width)
	body := m.View(width, height-lipgloss.Height(helpLine))
	out := composeScreen(body, helpLine, height)
	goldenAssert(t, "conversations.txt", out)
}

// TestConversationsModalGolden snapshots the `p` transcript preview
// overlay at 120x40 — the design-system breakpoint where the wide
// layout kicks in. Fixed messages + the same fixed-relative-time
// fakeConversations() seed keep the snapshot deterministic.
func TestConversationsModalGolden(t *testing.T) {
	const width, height = 120, 40
	st := styles.Default()

	target := fakeConversations()[0] // claude row
	var overlay conversationPreviewOverlay
	overlay.Open(target)
	overlay.SetMessages(target.ID, []conversations.Message{
		{Role: "user", Content: "Walk me through the auth-redesign migration plan."},
		{Role: "assistant", Content: "Sure. We're swapping the legacy session-token middleware for a passkey-backed flow. The plan is in three phases:\n\n1. Bring up the new endpoints behind a feature flag.\n2. Dual-write for one week so we can compare.\n3. Cut the legacy code path."},
	})

	out := overlay.View(st, width, height)
	goldenAssert(t, "conversations_modal.txt", out)
}

// TestProjectsGolden snapshots the Projects screen at 120x40 with a
// small fixed set of fake projects. No live remote hosts so the
// "on local" group header is the only host section that renders.
func TestProjectsGolden(t *testing.T) {
	const width, height = 120, 40
	st := styles.Default()
	km := DefaultKeymap()

	m := newProjects(st, km)
	m.projects = []project.Project{
		{
			Name: "ccmux", Path: "/Users/me/repos/ccmux",
			HasGit: true, HasCM: true, HasDocs: true,
			Agent: agent.IDClaude,
		},
		{
			Name: "auth-redesign", Path: "/Users/me/repos/auth-redesign",
			HasGit: true, HasCM: true,
			Agent: agent.IDClaude,
		},
		{
			Name: "parser", Path: "/Users/me/repos/parser",
			HasGit: true,
			Agent:  agent.IDCodex,
		},
	}

	helpLine := renderHelpBarFor(st, m.HelpBarProps(width), width)
	body := m.View(width, height-lipgloss.Height(helpLine))
	out := composeScreen(body, helpLine, height)
	goldenAssert(t, "projects.txt", out)
}

// TestProjectsFilterGolden snapshots the Projects screen at 120x40
// in filter-active state. The filter prompt + match count line, the
// narrowed visible set, and the help bar all differ from the no-
// filter golden — this catches drift in either surface.
func TestProjectsFilterGolden(t *testing.T) {
	const width, height = 120, 40
	st := styles.Default()
	km := DefaultKeymap()

	m := newProjects(st, km)
	m.projects = []project.Project{
		{
			Name: "ccmux", Path: "/Users/me/repos/ccmux",
			HasGit: true, HasCM: true, HasDocs: true,
			Agent: agent.IDClaude,
		},
		{
			Name: "ccmux-website", Path: "/Users/me/repos/ccmux-website",
			HasGit: true, HasCM: true,
			Agent: agent.IDClaude,
		},
		{
			Name: "auth-redesign", Path: "/Users/me/repos/auth-redesign",
			HasGit: true, HasCM: true,
			Agent: agent.IDClaude,
		},
		{
			Name: "parser", Path: "/Users/me/repos/parser",
			HasGit: true,
			Agent:  agent.IDCodex,
		},
	}
	m.loaded = true
	m.enterFilter()
	m.filter.SetValue("ccmux")

	helpLine := renderHelpBarFor(st, m.HelpBarProps(width), width)
	body := m.View(width, height-lipgloss.Height(helpLine))
	out := composeScreen(body, helpLine, height)
	goldenAssert(t, "projects_filter.txt", out)
}

// TestNotesGolden snapshots the Notes screen at 120x40 with a
// project that has a handful of fake entries. The preview pane will
// render the placeholder (no file actually loaded) so the snapshot
// doesn't depend on glamour theme output.
func TestNotesGolden(t *testing.T) {
	const width, height = 120, 40
	st := styles.Default()
	km := DefaultKeymap()

	m := newNotes(st, km)
	p := &project.Project{
		Name: "ccmux",
		Path: "/Users/me/repos/ccmux",
	}
	m.project = p
	m.entries = []notes.Entry{
		{Rel: "README.md", Dir: "", Display: "README.md",
			Modified: time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)},
		{Rel: "CLAUDE.md", Dir: "", Display: "CLAUDE.md",
			Modified: time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)},
		{Rel: "docs/01_Specs/00_Vision.md", Dir: "docs/01_Specs", Display: "00_Vision.md",
			Modified: time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)},
		{Rel: "docs/02_Architecture/00_System_Design.md", Dir: "docs/02_Architecture", Display: "00_System_Design.md",
			Modified: time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)},
	}

	helpLine := renderHelpBarFor(st, m.HelpBarProps(width), width)
	body := m.View(width, height-lipgloss.Height(helpLine))
	out := composeScreen(body, helpLine, height)
	goldenAssert(t, "notes.txt", out)
}

// TestSettingsGolden snapshots the Settings screen at 120x40. Both
// the config path and the Projects.Root default are pinned to fixed
// values so the snapshot doesn't drift across machines (config.Defaults
// computes Projects.Root from $HOME, which is /Users/<user>/ on a Mac
// and /home/runner/ on the Ubuntu CI runner). Version is set to a
// fixed v0.0.0-golden string.
func TestSettingsGolden(t *testing.T) {
	const width, height = 120, 40
	st := styles.Default()
	km := DefaultKeymap()

	cfg := config.Defaults()
	cfg.Subscription.Tier = "max5x"
	cfg.Projects.Root = "/Users/me/Projects"
	m := newSettings(st, km, cfg, "v0.0.0-golden")
	m.SetCfgPath("/Users/me/.config/ccmux/config.toml")

	helpLine := renderHelpBarFor(st, m.HelpBarProps(width), width)
	body := m.View(width, height-lipgloss.Height(helpLine))
	out := composeScreen(body, helpLine, height)
	goldenAssert(t, "settings.txt", out)
}
