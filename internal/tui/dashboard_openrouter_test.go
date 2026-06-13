package tui

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/claudeusage"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tui/styles"
	"github.com/skzv/ccmux/internal/usage"
)

// TestDashboard_OtherAgents_ShowsRowsForAgentsWithUsage — the second-
// wave agents render a usage row only when they have data. An agent the
// user doesn't run never appears (the panel sizes to actual use).
func TestDashboard_OtherAgents_ShowsRowsForAgentsWithUsage(t *testing.T) {
	m := dashboardWithUsage(t)
	m.SetOtherUsage([]usage.NamedSummary{
		{Agent: "opencode", Summary: usage.AgentSummary{HasData: true, Prompts: 12, InputTokens: 50000, OutputTokens: 8000}},
	})
	out := m.usagePanel(120)
	if !strings.Contains(out, "OpenCode · recent") {
		t.Errorf("OpenCode usage row missing when it has data:\n%s", out)
	}
	if !strings.Contains(out, "12") {
		t.Errorf("OpenCode prompt count missing:\n%s", out)
	}
	// An agent NOT in the list must not appear.
	if strings.Contains(out, "Kimi · recent") {
		t.Errorf("Kimi row shown though it has no usage:\n%s", out)
	}
}

// TestDashboard_OtherAgents_EmptyByDefault — with no second-wave usage,
// the panel shows none of those rows (no wall of placeholders).
func TestDashboard_OtherAgents_EmptyByDefault(t *testing.T) {
	m := dashboardWithUsage(t)
	out := m.usagePanel(120)
	for _, name := range []string{"OpenCode · recent", "Kimi · recent", "Droid · recent"} {
		if strings.Contains(out, name) {
			t.Errorf("second-wave row %q shown with no usage data:\n%s", name, out)
		}
	}
}

// dashboardWithUsage returns a dashboard with a minimal Claude
// aggregate so usagePanel renders its full (non-loading) layout — the
// OpenRouter section only appears once the panel is past the loading
// placeholder.
func dashboardWithUsage(t *testing.T) dashboardModel {
	t.Helper()
	m := newDashboard(styles.Default(), DefaultKeymap())
	m.SetUsage(&claudeusage.Aggregate{Messages: 1, UserPrompts: 1})
	return m
}

// TestDashboard_OpenRouter_HiddenWhenDisabled — the default (no
// [openrouter] config) renders no OpenRouter row. A spend line for an
// unconfigured integration would be noise.
func TestDashboard_OpenRouter_HiddenWhenDisabled(t *testing.T) {
	m := dashboardWithUsage(t)
	// no SetOpenRouterSpend → Enabled=false
	out := m.usagePanel(120)
	if strings.Contains(out, "OpenRouter") {
		t.Errorf("OpenRouter row shown when integration is disabled:\n%s", out)
	}
}

// TestDashboard_OpenRouter_ShowsSpendAndCap — an enabled, capped key
// renders spend + remaining/limit.
func TestDashboard_OpenRouter_ShowsSpendAndCap(t *testing.T) {
	m := dashboardWithUsage(t)
	m.SetOpenRouterSpend(daemon.OpenRouterSpend{
		Enabled:   true,
		Usage:     12.50,
		Limit:     50,
		Remaining: 37.50,
	})
	out := m.usagePanel(120)
	if !strings.Contains(out, "OpenRouter") {
		t.Fatalf("OpenRouter section missing when enabled:\n%s", out)
	}
	for _, want := range []string{"$12.50", "spent", "$37.50", "of $50.00 left"} {
		if !strings.Contains(out, want) {
			t.Errorf("OpenRouter row missing %q:\n%s", want, out)
		}
	}
}

// TestDashboard_OpenRouter_UncappedKey — a key with no limit shows
// spend but "uncapped" instead of a misleading remaining figure.
func TestDashboard_OpenRouter_UncappedKey(t *testing.T) {
	m := dashboardWithUsage(t)
	m.SetOpenRouterSpend(daemon.OpenRouterSpend{
		Enabled:   true,
		Usage:     8.0,
		Limit:     0,
		Remaining: -1, // uncapped sentinel
	})
	out := m.usagePanel(120)
	if !strings.Contains(out, "$8.00") {
		t.Errorf("spend missing for uncapped key:\n%s", out)
	}
	if !strings.Contains(out, "uncapped") {
		t.Errorf("uncapped key should say 'uncapped', not a remaining figure:\n%s", out)
	}
	if strings.Contains(out, "left") {
		t.Errorf("uncapped key must not show a 'left' figure:\n%s", out)
	}
}

// TestDashboard_OpenRouter_ErrorSurfaced — a configured-but-failing
// fetch shows the error, not a silent blank.
func TestDashboard_OpenRouter_ErrorSurfaced(t *testing.T) {
	m := dashboardWithUsage(t)
	m.SetOpenRouterSpend(daemon.OpenRouterSpend{
		Enabled: true,
		ErrMsg:  "unauthorized (check api_key)",
	})
	out := m.usagePanel(120)
	if !strings.Contains(out, "unavailable") {
		t.Errorf("OpenRouter error state not surfaced:\n%s", out)
	}
	if !strings.Contains(out, "check api_key") {
		t.Errorf("underlying error not shown:\n%s", out)
	}
}

// TestDashboard_OpenRouter_FreeTierTag — a free-tier key gets a tag so
// the user knows the spend is against free credits.
func TestDashboard_OpenRouter_FreeTierTag(t *testing.T) {
	m := dashboardWithUsage(t)
	m.SetOpenRouterSpend(daemon.OpenRouterSpend{
		Enabled:    true,
		Usage:      0.10,
		Remaining:  -1,
		IsFreeTier: true,
	})
	out := m.usagePanel(120)
	if !strings.Contains(out, "free tier") {
		t.Errorf("free-tier key should be tagged:\n%s", out)
	}
}
