package usage

import (
	"time"

	"github.com/skzv/ccmux/internal/claudeusage"
)

// ClaudeBlock is Claude's current subscription session block: the
// 5-hour Pro/Max quota window the dashboard's quota bar and "resets in"
// line show, as opposed to the plain rolling window every AgentSummary
// covers. Blocks start at the hour the first message after the previous
// block fell in (see claudeusage.Walk), so the two disagree about what
// counts and when it resets.
type ClaudeBlock struct {
	// Active is false when no block is running (idle for a while, or the
	// last one ran out): everything below is then zero, and the next
	// message opens a new block.
	Active bool
	// Start is the block's hour-floored start and ResetAt its end, when
	// the quota resets; both zero when !Active.
	Start, ResetAt time.Time
	// Length is the block length (claudeusage.SessionBlock).
	Length time.Duration
	// Prompts is what the dashboard's quota bar counts: prompts the user
	// sent in the block. Messages is the assistant responses in it.
	Prompts, Messages int
	Tokens            claudeusage.Tokens
	EstimatedCost     float64 // USD at published API rates
}

// WalkClaudeBlock reads Claude's transcripts for the active session
// block. It is the one walk behind both the dashboard's Claude usage
// panel and the daemon's /v1/usage claude_block, so the TUI, the phone
// and `ccmux usage` report the same block.
func WalkClaudeBlock() (*claudeusage.Aggregate, error) {
	return claudeusage.Walk(claudeusage.SessionBlock)
}

// ClaudeBlockOf summarizes a WalkClaudeBlock result the way the
// dashboard reads it: prompts from UserPrompts, the reset from ResetAt,
// the cost from EstimatedCost. A nil aggregate is no block.
func ClaudeBlockOf(agg *claudeusage.Aggregate) ClaudeBlock {
	b := ClaudeBlock{Length: claudeusage.SessionBlock}
	if agg == nil {
		return b
	}
	b.Active = !agg.BlockStart.IsZero()
	b.Start = agg.BlockStart
	b.ResetAt = agg.ResetAt(claudeusage.SessionBlock)
	b.Prompts, b.Messages = agg.UserPrompts, agg.Messages
	b.Tokens = agg.Total
	b.EstimatedCost = agg.EstimatedCost()
	return b
}
