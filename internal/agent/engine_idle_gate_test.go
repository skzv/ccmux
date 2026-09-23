package agent

import (
	"testing"
	"time"
)

// TestClaudeEngine_PromptFrameIdleGate — finding: the engine's
// claude_prompt_frame rule returned needs_input the instant a frame
// glyph appeared, dropping the legacy idle-delay gate (claude.Classify
// requires idleNeedsInput of pane silence before believing the prompt
// frame). One capture racing a redraw → instant needs_input → spurious
// push. The rule now carries require_idle and engineClassify holds the
// state at active until the pane has been quiet for the threshold.
func TestClaudeEngine_PromptFrameIdleGate(t *testing.T) {
	frame := "some output\n╭──────────╮\n│ >        │\n╰──────────╯"
	idle := 3 * time.Second

	if got := (Claude{}).ClassifyWithTitle(frame, "", time.Now(), idle); got != StateActive {
		t.Errorf("prompt frame with a fresh pane change = %v, want active (idle gate must hold)", got)
	}
	if got := (Claude{}).ClassifyWithTitle(frame, "", time.Now().Add(-time.Hour), idle); got != StateNeedsInput {
		t.Errorf("prompt frame after quiet period = %v, want needs_input", got)
	}
}

// TestClaudeEngine_LoneRoundedGlyphIsNotAPrompt — a single `╰` on the
// last line (a capture racing a partial frame redraw) must never
// classify needs_input, no matter how long the pane has been quiet as
// a "prompt": it falls through to the body classifier, which calls
// this shape idle.
func TestClaudeEngine_LoneRoundedGlyphIsNotAPrompt(t *testing.T) {
	pane := "some output\n╰"
	got := (Claude{}).ClassifyWithTitle(pane, "", time.Now().Add(-time.Hour), 3*time.Second)
	if got == StateNeedsInput {
		t.Errorf("lone ╰ classified %v — a degenerate capture must not page the user", got)
	}
	if got := (Claude{}).ClassifyWithTitle(pane, "", time.Now(), 3*time.Second); got == StateNeedsInput {
		t.Errorf("lone ╰ with recent change classified needs_input")
	}
}

// TestSecondWaveBlockedRules_IdleGated — kiro / qoder / amp / opencode
// / kilo approval rules had no require_idle, so approval-shaped text on
// screen flipped needs_input (and rang the bell) the instant it was
// captured, even while the pane was still repainting. They now wait out
// the idle threshold like every other body-shaped blocked rule.
func TestSecondWaveBlockedRules_IdleGated(t *testing.T) {
	const idle = 3 * time.Second
	cases := []struct {
		agent Agent
		pane  string
	}{
		{Kiro{}, "output\nThis action requires approval.\n  yes, single permission"},
		{Qoder{}, "output\nwaiting for user confirmation"},
		{Amp{}, "output\nAllow editing file: src/main.go?"},
		{OpenCode{}, "output\n△ Permission required\n  Allow once   Allow always   Reject"},
		{Kilo{}, "output\n△ Permission required\n  Allow once   Allow always   Reject"},
	}
	for _, tc := range cases {
		t.Run(string(tc.agent.ID()), func(t *testing.T) {
			if got := ClassifyState(tc.agent, tc.pane, "", time.Now(), idle); got != StateActive {
				t.Errorf("approval text on a pane that just changed = %v, want active (idle gate)", got)
			}
			if got := ClassifyState(tc.agent, tc.pane, "", time.Now().Add(-time.Hour), idle); got != StateNeedsInput {
				t.Errorf("approval text on a quiet pane = %v, want needs_input", got)
			}
		})
	}
}
