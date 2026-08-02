package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/cursorusage"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestAgentsClaudeGolden snapshots the Agents → Claude sub-tab at
// 120x40 with an isolated $CLAUDE_CONFIG_DIR so the rendered paths
// don't drift across hosts. The settings file is empty; the picker
// is closed; what the user sees is the default model / effort /
// safety + the config-file paths under the two grouping headings.
func TestAgentsClaudeGolden(t *testing.T) {
	const width, height = 120, 40
	st := styles.Default()
	km := DefaultKeymap()

	// Pin the rendered Claude paths to a stable string so the golden
	// doesn't drift across machines / test runs. The directory does
	// not need to exist — claudeconfig.ReadSettings / ListCommands /
	// ListSkills gracefully tolerate a missing root and surface the
	// "(none)" / "(no override)" treatments the snapshot captures.
	t.Setenv("CLAUDE_CONFIG_DIR", "/Users/me/.claude")
	t.Setenv("HOME", "/Users/me")
	// Hermetic: a developer shell may export ANTHROPIC_MODEL (which the
	// model row reflects as a shell override). Clear it so the golden is
	// stable regardless of the runner's environment.
	t.Setenv("ANTHROPIC_MODEL", "")

	m := newAgents(st, km)
	m.active = agent.IDClaude
	m.claude.reload()

	helpLine := renderHelpBarFor(st, m.HelpBarProps(width), width)
	body := m.View(width, height-lipgloss.Height(helpLine))
	out := composeScreen(body, helpLine, height)
	goldenAssert(t, "agents_claude.txt", out)
}

// TestAgentsCursorGolden snapshots the Agents → Cursor sub-tab with
// a deterministic Summary (3 conversations, 3 top models, 70 AI
// lines this week, last activity at a fixed timestamp). SetSummary
// bypasses the spinner + SQLite read so the snapshot is hermetic.
// TZ is pinned to UTC so the rendered "last activity" timestamp
// matches across local-dev (PT/CET/etc.) and CI runners.
func TestAgentsCursorGolden(t *testing.T) {
	const width, height = 120, 40
	st := styles.Default()
	km := DefaultKeymap()

	t.Setenv("HOME", "/Users/me")
	t.Setenv("TZ", "UTC")
	time.Local = time.UTC

	m := newAgents(st, km)
	m.active = agent.IDCursor

	now := time.Date(2026, 5, 27, 14, 30, 0, 0, time.UTC)
	m.cursor.SetSummary(cursorusage.Summary{
		Conversations: 3,
		Models:        []string{"claude-sonnet-4-6", "gpt-5", "gemini-3-pro"},
		AILinesLast7d: 70,
		LastActivity:  now.Add(-15 * time.Minute),
	}, now)

	helpLine := renderHelpBarFor(st, m.HelpBarProps(width), width)
	body := m.View(width, height-lipgloss.Height(helpLine))
	out := composeScreen(body, helpLine, height)
	goldenAssert(t, "agents_cursor.txt", out)
}

// TestAgentsCursorEmptyGolden snapshots the Agents → Cursor sub-tab
// when `~/.cursor/ai-tracking/ai-code-tracking.db` is absent. The
// sub-tab MUST render the muted "Cursor not detected" placeholder
// instead of an error or an empty body.
func TestAgentsCursorEmptyGolden(t *testing.T) {
	const width, height = 120, 40
	st := styles.Default()
	km := DefaultKeymap()

	t.Setenv("HOME", "/Users/me")

	m := newAgents(st, km)
	m.active = agent.IDCursor

	now := time.Date(2026, 5, 27, 14, 30, 0, 0, time.UTC)
	m.cursor.SetNotInstalled(now)

	helpLine := renderHelpBarFor(st, m.HelpBarProps(width), width)
	body := m.View(width, height-lipgloss.Height(helpLine))
	out := composeScreen(body, helpLine, height)
	goldenAssert(t, "agents_cursor_empty.txt", out)
}

// TestAgentsClaudeBrowserGolden snapshots the Claude sub-tab's
// browser modal (`b` key) with a hand-built deterministic section
// list. The browser is the central new affordance — left list of
// hooks/MCP/commands/skills, right pane preview — so a golden locks
// the visual contract.
func TestAgentsClaudeBrowserGolden(t *testing.T) {
	const width, height = 120, 40
	st := styles.Default()
	km := DefaultKeymap()

	t.Setenv("CLAUDE_CONFIG_DIR", "/Users/me/.claude")
	t.Setenv("HOME", "/Users/me")
	// Hermetic: a developer shell may export ANTHROPIC_MODEL (which the
	// model row reflects as a shell override). Clear it so the golden is
	// stable regardless of the runner's environment.
	t.Setenv("ANTHROPIC_MODEL", "")

	m := newAgents(st, km)
	m.active = agent.IDClaude

	sections := []agentBrowserSection{
		{Title: "Hooks", Color: st.P.Peach, Items: []agentBrowserItem{
			{Label: "SessionStart", Trailing: "1 hook(s)", Preview: "SessionStart\n\n  command: '/opt/homebrew/bin/moshi-hook' claude-hook"},
			{Label: "Stop", Trailing: "1 hook(s)", Preview: "Stop\n\n  command: '/opt/homebrew/bin/moshi-hook' claude-hook"},
		}},
		{Title: "MCP servers", Color: st.P.Sky, Items: []agentBrowserItem{
			{Label: "github", Trailing: "stdio", Preview: "github\n\n  type: stdio\n  command: gh-mcp\n  env keys: GH_TOKEN"},
			{Label: "linear", Trailing: "http", Preview: "linear\n\n  type: http\n  url: https://mcp.linear.app/sse"},
		}},
		{Title: "Commands", Color: st.P.Green, Items: []agentBrowserItem{
			{Label: "/commit", Preview: "/commit\n\nCreate a scoped git commit from local changes.\n\n  source: ~/.claude/commands/commit.md"},
		}},
		{Title: "Skills", Color: st.P.Mauve, Items: []agentBrowserItem{
			{Label: "frontend-expert", Preview: "frontend-expert\n\nCreate distinctive frontend interfaces.\n\n  source: ~/.claude/skills/frontend-expert/SKILL.md"},
		}},
	}
	m.claude.browser.SetSections("Claude Code configured", sections)

	helpLine := renderHelpBarFor(st, m.HelpBarProps(width), width)
	body := m.View(width, height-lipgloss.Height(helpLine))
	out := composeScreen(body, helpLine, height)
	goldenAssert(t, "agents_claude_browser.txt", out)
}
