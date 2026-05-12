package tui

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
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
	if strings.Contains(got, "[codex]") || strings.Contains(got, "[gemini]") {
		t.Errorf("non-default tags should not appear on claude row:\n%s", got)
	}
}

// TestRenderSessionLine_AgentTag_NonDefault — when the session is
// running Codex or Gemini, the row gets a small `[codex]` / `[gemini]`
// tag in muted styling so the user can tell at a glance which agent
// is driving which row.
func TestRenderSessionLine_AgentTag_NonDefault(t *testing.T) {
	st := styles.Default()
	for _, id := range []agent.ID{agent.IDCodex, agent.IDGemini} {
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
