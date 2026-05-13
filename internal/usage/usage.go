// Package usage is the per-agent token-usage walker layer. Today it's
// a thin wrapper:
//
//   - Claude: delegates to internal/claudeusage.Walk (existing rich
//     walker; the dashboard's main Claude panel still uses that
//     package directly for its 5h-window quota bar + per-project
//     drill-down).
//   - Codex, Gemini: stubs that return zero-valued AgentSummary.
//     They'll grow real implementations once we have real
//     transcripts to fixture against. See
//     docs/01_Specs/02_Multi_Agent.md, Phase-4 deferred items.
//
// The dashboard uses this package to render compact per-agent rows
// beneath the Claude panel so users adopting Codex / Gemini see at a
// glance whether ccmux has discovered their transcripts yet. Empty
// rows still get rendered with an "install hint" so the first-time
// user knows what to do.
//
// Why a separate type from claudeusage.Aggregate: Claude's
// transcript shape carries cache-create/cache-read tokens and
// rolling-window quota semantics that don't map to Codex/Gemini's
// pricing model. AgentSummary keeps only the fields that mean the
// same thing across every agent: prompts, input/output tokens, and
// an API-rates cost estimate.
package usage

import (
	"time"

	"github.com/skzv/ccmux/internal/claudeusage"
)

// AgentSummary is the cross-agent usage roll-up the dashboard's
// per-agent panel shows. HasData=false renders as a placeholder row
// ("no transcripts yet — install via …"); HasData=true renders the
// token + cost details.
type AgentSummary struct {
	// HasData distinguishes "no walker yet implemented" /
	// "no transcripts on disk" from "real walker but empty window".
	// Both cases render compact, but HasData=false also surfaces
	// the install hint.
	HasData bool

	Window        time.Duration
	Prompts       int     // user-initiated turns in the window
	InputTokens   int     // billed input tokens
	OutputTokens  int     // billed output tokens
	EstimatedCost float64 // USD at the agent's published API rates
}

// TotalTokens returns the input+output sum. Cache tokens (Claude-
// specific) are accounted in the rich Claude panel separately.
func (s AgentSummary) TotalTokens() int {
	return s.InputTokens + s.OutputTokens
}

// WalkClaude returns the cross-agent summary for Claude Code by
// delegating to the existing claudeusage walker. This is the only
// path that returns real data today; the rich Claude panel in the
// dashboard uses claudeusage.Aggregate directly, this function is
// here so the "all three agents" rendering loop has a uniform API.
func WalkClaude(window time.Duration) (AgentSummary, error) {
	agg, err := claudeusage.Walk(window)
	if err != nil || agg == nil {
		return AgentSummary{}, err
	}
	// HasData reflects whether we actually FOUND usage, not whether
	// the walker ran without error. An empty ~/.claude/projects/
	// (fresh install, never used Claude) returns a valid-but-zero
	// Aggregate; we want the dashboard to show the install hint for
	// that case, same as for Codex/Gemini stubs. Messages > 0 is
	// the honest signal — UserPrompts can be 0 even when there are
	// transcripts if they're all tool-result follow-ups.
	if agg.Messages == 0 {
		return AgentSummary{}, nil
	}
	return AgentSummary{
		HasData:       true,
		Window:        window,
		Prompts:       agg.UserPrompts,
		InputTokens:   agg.Total.Input,
		OutputTokens:  agg.Total.Output,
		EstimatedCost: agg.EstimatedCost(),
	}, nil
}

// WalkCodex is the placeholder until ~/.codex/sessions/ transcripts
// are fixtured. When that lands, this function will:
//
//  1. Glob ~/.codex/sessions/*.jsonl (or whatever shape ships)
//  2. Parse per-message usage records (Codex emits OpenAI-API-style
//     responses with prompt_tokens / completion_tokens)
//  3. Filter to messages within `window`
//  4. Multiply by the published per-1M-token rate for the model
//     reported in each record
//
// Today it returns HasData=false so the dashboard knows to render
// the install-hint placeholder instead of a "0 tokens" row.
func WalkCodex(window time.Duration) (AgentSummary, error) {
	return AgentSummary{}, nil
}

// WalkGemini mirrors WalkCodex. Gemini's transcripts live under
// ~/.gemini/conversations/ in JSON form; the usage record shape
// (likely `usageMetadata.{prompt,candidates,total}TokenCount`)
// surfaces with the Gemini API's own schema, which we'll fixture
// against real files when they exist.
func WalkGemini(window time.Duration) (AgentSummary, error) {
	return AgentSummary{}, nil
}
