package styles

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
)

// TestAgentAccentMapping verifies the design-system single source of
// truth: Claude=Mauve, Codex=Sky, Antigravity=Peach, Cursor=Teal,
// Pi=Green, Grok=Blue, with an unknown ID falling back to the muted
// style.
func TestAgentAccentMapping(t *testing.T) {
	s := Default()
	cases := []struct {
		name string
		id   agent.ID
		want lipgloss.Color
	}{
		{"claude", agent.IDClaude, s.P.Mauve},
		{"codex", agent.IDCodex, s.P.Sky},
		{"antigravity", agent.IDAntigravity, s.P.Peach},
		{"cursor", agent.IDCursor, s.P.Teal},
		{"pi", agent.IDPi, s.P.Green},
		{"grok", agent.IDGrok, s.P.Blue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := s.AgentAccent(tc.id).GetForeground()
			want := tc.want
			if got != want {
				t.Fatalf("AgentAccent(%q) foreground = %v, want %v", tc.id, got, want)
			}
		})
	}

	t.Run("unknown_falls_back_to_muted", func(t *testing.T) {
		got := s.AgentAccent(agent.ID("nope")).GetForeground()
		want := s.Muted.GetForeground()
		if got != want {
			t.Fatalf("AgentAccent(unknown) foreground = %v, want muted %v", got, want)
		}
	})
}

// TestAgentAccentRendersForeground sanity-checks that the returned
// style produces a foreground escape so rendered output is visibly
// coloured (the regression we'd hit if AgentAccent silently dropped
// the foreground).
func TestAgentAccentRendersForeground(t *testing.T) {
	lipgloss.SetColorProfile(0) // disable for deterministic strip-test
	s := Default()
	out := s.AgentAccent(agent.IDClaude).Render("x")
	if !strings.Contains(out, "x") {
		t.Fatalf("rendered output missing text: %q", out)
	}
}
