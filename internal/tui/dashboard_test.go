package tui

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
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

// TestRenderAgentUsageRow_NoData — when the cross-agent walker
// hasn't found transcripts (which is the case for Codex/Antigravity
// today, stub walkers), the dashboard row must show the install
// hint inline. A silent empty row would let a new Codex/Antigravity
// user assume the panel is broken instead of seeing the next step.
func TestRenderAgentUsageRow_NoData(t *testing.T) {
	st := styles.Default()
	got := renderAgentUsageRow(st, "Codex", usage.AgentSummary{HasData: false},
		"`npm i -g @openai/codex`")
	for _, want := range []string{
		"Codex",
		"no transcripts yet",
		"npm i -g @openai/codex",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no-data row missing %q in:\n%s", want, got)
		}
	}
}

// TestRenderAgentUsageRow_WithData — when the walker reports real
// usage (HasData=true), the row shows prompts + input/output tokens
// + an optional cost estimate, NOT the install hint.
func TestRenderAgentUsageRow_WithData(t *testing.T) {
	st := styles.Default()
	s := usage.AgentSummary{
		HasData:       true,
		Prompts:       42,
		InputTokens:   1500,
		OutputTokens:  3700,
		EstimatedCost: 0.123,
	}
	got := renderAgentUsageRow(st, "Codex", s, "`npm i -g @openai/codex`")
	for _, want := range []string{
		"Codex",
		"42 prompts",
		"in",
		"out",
		"$0.12",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("with-data row missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "no transcripts yet") {
		t.Errorf("with-data row leaked the no-data hint:\n%s", got)
	}
	if strings.Contains(got, "npm i -g") {
		t.Errorf("with-data row leaked the install hint:\n%s", got)
	}
}

// TestRenderAgentUsageRow_ZeroCostOmittedWhenWithData — when the
// estimator gives 0 (e.g. cache-only Claude usage that resolves to
// $0 at API rates), the row should not show "~$0.00" because that
// looks like an error. Skip the cost suffix entirely.
func TestRenderAgentUsageRow_ZeroCostOmittedWhenWithData(t *testing.T) {
	st := styles.Default()
	got := renderAgentUsageRow(st, "Antigravity", usage.AgentSummary{
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
