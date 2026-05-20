package tui

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/selfupdate"
	"github.com/skzv/ccmux/internal/tui/styles"
	"github.com/skzv/ccmux/internal/usage"
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

// TestRenderAgentUsageBlock_NoData — when the cross-agent walker
// hasn't found transcripts, the dashboard block must show the install
// hint. A silent empty block would let a new Codex/Antigravity user
// assume the panel is broken instead of seeing the next step.
func TestRenderAgentUsageBlock_NoData(t *testing.T) {
	st := styles.Default()
	got := renderAgentUsageBlock(st, "Codex", usage.AgentSummary{HasData: false},
		"`npm i -g @openai/codex`")
	for _, want := range []string{
		"Codex usage",
		"no transcripts yet",
		"npm i -g @openai/codex",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no-data block missing %q in:\n%s", want, got)
		}
	}
}

// TestRenderAgentUsageBlock_WithData — when the walker reports real
// usage (HasData=true), the block surfaces prompts + tokens + cost
// in the same Claude-shaped layout, NOT the install hint.
func TestRenderAgentUsageBlock_WithData(t *testing.T) {
	st := styles.Default()
	s := usage.AgentSummary{
		HasData:       true,
		Prompts:       42,
		InputTokens:   1500,
		OutputTokens:  3700,
		EstimatedCost: 0.123,
	}
	got := renderAgentUsageBlock(st, "Codex", s, "`npm i -g @openai/codex`")
	for _, want := range []string{
		"Codex usage",
		"42 prompts",
		"tokens",
		"in",
		"out",
		"$0.12",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("with-data block missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "no transcripts yet") {
		t.Errorf("with-data block leaked the no-data hint:\n%s", got)
	}
	if strings.Contains(got, "npm i -g") {
		t.Errorf("with-data block leaked the install hint:\n%s", got)
	}
}

// TestRenderAgentUsageBlock_ZeroCostOmittedWhenWithData — when the
// estimator gives 0, the block should not show "~$0.00" because that
// looks like an error. Skip the cost row entirely.
func TestRenderAgentUsageBlock_ZeroCostOmittedWhenWithData(t *testing.T) {
	st := styles.Default()
	got := renderAgentUsageBlock(st, "Antigravity", usage.AgentSummary{
		HasData:       true,
		Prompts:       1,
		InputTokens:   100,
		OutputTokens:  200,
		EstimatedCost: 0,
	}, "`curl -fsSL https://antigravity.google/cli/install.sh | bash`")
	if strings.Contains(got, "$0.00") {
		t.Errorf("zero cost should be omitted, got:\n%s", got)
	}
}

// TestRenderAgentUsageBlock_AntigravityOpaqueTokens — Antigravity has
// HasData=true (we counted .pb files) but Input/Output stay 0 because
// the protobuf is opaque. The block must surface the "tokens
// unavailable" note rather than silently rendering "0K in · 0K out"
// (which would look like the agent produced literally no output).
func TestRenderAgentUsageBlock_AntigravityOpaqueTokens(t *testing.T) {
	st := styles.Default()
	got := renderAgentUsageBlock(st, "Antigravity", usage.AgentSummary{
		HasData:      true,
		Prompts:      3,
		InputTokens:  0,
		OutputTokens: 0,
	}, "`curl …`")
	// Headline still shows prompts.
	if !strings.Contains(got, "3 prompts") {
		t.Errorf("antigravity block should still show prompt count: %s", got)
	}
	// "unavailable" note replaces the token line so the user doesn't
	// read "0K in · 0K out" as "the agent did nothing."
	if !strings.Contains(got, "unavailable") {
		t.Errorf("antigravity zero-token block missing 'unavailable' note:\n%s", got)
	}
	// And the literal "0 in" must NOT appear.
	if strings.Contains(got, "0 in") {
		t.Errorf("antigravity block should not render literal '0 in':\n%s", got)
	}
}

// TestDashboard_UpdateBanner_ShownWhenBehind — once App pushes a
// positive update check, the hero panel must carry the banner.
// Pluralization is pinned because "1 commit" vs "3 commits" is the
// kind of thing a refactor silently breaks.
func TestDashboard_UpdateBanner_ShownWhenBehind(t *testing.T) {
	st := styles.Default()
	cases := []struct {
		behind int
		want   string
	}{
		{1, "1 commit"},
		{3, "3 commits"},
		{12, "12 commits"},
	}
	for _, tc := range cases {
		m := newDashboard(st, DefaultKeymap())
		m.SetUpdateAvailable(selfupdate.Result{Behind: tc.behind, Branch: "main"})
		hero := m.heroPanel(120)
		if !strings.Contains(hero, "update available") {
			t.Errorf("behind=%d: hero missing the update banner:\n%s", tc.behind, hero)
		}
		if !strings.Contains(hero, tc.want) {
			t.Errorf("behind=%d: banner should say %q:\n%s", tc.behind, tc.want, hero)
		}
		if !strings.Contains(hero, "ccmux update") {
			t.Errorf("behind=%d: banner should tell the user to run `ccmux update`", tc.behind)
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

// TestDashboard_UpdateBanner_HiddenWhenUpToDate — the zero-value
// Result (no check, or check found 0 behind) must render NO banner.
// A banner that shows when there's no update would train users to
// ignore it.
func TestDashboard_UpdateBanner_HiddenWhenUpToDate(t *testing.T) {
	m := newDashboard(styles.Default(), DefaultKeymap())
	// No SetUpdateAvailable call — zero value.
	if strings.Contains(m.heroPanel(120), "update available") {
		t.Error("hero panel shows an update banner with no update pending")
	}
	// Explicitly Behind=0 must also stay silent.
	m.SetUpdateAvailable(selfupdate.Result{Behind: 0, Branch: "main"})
	if strings.Contains(m.heroPanel(120), "update available") {
		t.Error("Behind=0 should not render the banner")
	}
}
