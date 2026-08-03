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
