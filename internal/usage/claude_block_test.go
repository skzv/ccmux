package usage

import (
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/claudeusage"
)

// TestClaudeBlockOf — the block summary reads an aggregate the way the
// dashboard's Claude panel does: UserPrompts for prompts, ResetAt for
// the reset, EstimatedCost for the cost; no aggregate, or one with no
// block running, is an inactive block.
func TestClaudeBlockOf(t *testing.T) {
	if b := ClaudeBlockOf(nil); b.Active || b.Length != claudeusage.SessionBlock || !b.ResetAt.IsZero() {
		t.Errorf("nil aggregate: %+v, want an inactive %v block", b, claudeusage.SessionBlock)
	}
	if b := ClaudeBlockOf(&claudeusage.Aggregate{Window: claudeusage.SessionBlock}); b.Active || !b.Start.IsZero() || !b.ResetAt.IsZero() {
		t.Errorf("no block running: %+v, want inactive with no times", b)
	}

	start := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	tokens := claudeusage.Tokens{Input: 1000, Output: 200, CacheCreation: 3000, CacheRead: 40000}
	agg := &claudeusage.Aggregate{
		Window:      claudeusage.SessionBlock,
		BlockStart:  start,
		Messages:    9,
		UserPrompts: 4,
		Total:       tokens,
		ByModel:     map[string]*claudeusage.Tokens{"claude-sonnet-4-6": &tokens},
	}
	b := ClaudeBlockOf(agg)
	if !b.Active || !b.Start.Equal(start) || !b.ResetAt.Equal(agg.ResetAt(5*time.Hour)) || !b.ResetAt.Equal(start.Add(5*time.Hour)) {
		t.Errorf("active block: %+v, want %v to %v", b, start, start.Add(5*time.Hour))
	}
	if b.Prompts != 4 || b.Messages != 9 || b.Tokens != tokens || b.EstimatedCost != agg.EstimatedCost() || b.EstimatedCost <= 0 {
		t.Errorf("active block counts: %+v, want 4 prompts, 9 messages, %+v, $%f", b, tokens, agg.EstimatedCost())
	}
}
