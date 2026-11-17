package main

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/openrouterusage"
	"github.com/skzv/ccmux/internal/usage"
)

// maxUsageWindow caps /v1/usage's ?window=. Every call walks every
// transcript touched inside the window, so an unbounded value
// (window=876000h) turns each request into a full-history scan.
const maxUsageWindow = 31 * 24 * time.Hour

// parseUsageWindow reads /v1/usage's ?window=: 5h when absent or
// invalid, clamped to maxUsageWindow.
func parseUsageWindow(q string) time.Duration {
	if d, err := time.ParseDuration(q); err == nil && d > 0 {
		return min(d, maxUsageWindow)
	}
	return 5 * time.Hour
}

// handleUsage returns per-agent token + cost activity over a rolling
// window (default 5 hours, override via ?window=2h, 24h, 30m, …), plus
// Claude's current 5-hour session block (claude_block), which the
// window doesn't change. The walkers are best-effort: a missing or
// corrupt transcript on one agent doesn't sink the others. iOS uses
// this for its dashboard usage card.
func (s *server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	window := parseUsageWindow(r.URL.Query().Get("window"))
	// Each walker is cheap and IO-bound; run them concurrently so a
	// slow disk doesn't serialize the reads. OpenRouter is a network
	// call, so it especially benefits from running alongside the rest.
	var (
		wg                 sync.WaitGroup
		claude, codex, ant usage.AgentSummary
		block              *daemon.ClaudeBlock
		others             []usage.NamedSummary
		orSpend            daemon.OpenRouterSpend
	)
	wg.Add(6)
	go func() { defer wg.Done(); claude, _ = usage.WalkClaude(window) }()
	go func() { defer wg.Done(); block = claudeBlock() }()
	go func() { defer wg.Done(); codex, _ = usage.WalkCodex(window) }()
	go func() { defer wg.Done(); ant, _ = usage.WalkAntigravity(window) }()
	go func() { defer wg.Done(); others = usage.WalkOthers(window) }()
	go func() { defer wg.Done(); orSpend = s.openRouterSpend(r.Context()) }()
	wg.Wait()
	writeJSON(w, daemon.AgentUsage{
		Claude:      toUsageSummary(claude),
		Codex:       toUsageSummary(codex),
		Antigravity: toUsageSummary(ant),
		OpenRouter:  orSpend,
		Others:      toOtherUsage(others),
		ClaudeBlock: block,
	})
}

// claudeBlock reads Claude's current session block the way the TUI's
// usage panel does (usage.WalkClaudeBlock), for /v1/usage: the phone's
// usage card and `ccmux usage` showed a rolling window that disagreed
// with the TUI's quota bar about tokens, cost and the reset time. nil
// when the transcripts can't be read.
func claudeBlock() *daemon.ClaudeBlock {
	agg, err := usage.WalkClaudeBlock()
	if err != nil {
		return nil
	}
	b := usage.ClaudeBlockOf(agg)
	return &daemon.ClaudeBlock{
		Active:              b.Active,
		Start:               b.Start,
		ResetAt:             b.ResetAt,
		BlockSeconds:        int(b.Length / time.Second),
		Prompts:             b.Prompts,
		Messages:            b.Messages,
		InputTokens:         b.Tokens.Input,
		OutputTokens:        b.Tokens.Output,
		CacheCreationTokens: b.Tokens.CacheCreation,
		CacheReadTokens:     b.Tokens.CacheRead,
		EstimatedCost:       b.EstimatedCost,
	}
}

// toOtherUsage maps the generic per-agent summaries to the wire shape.
func toOtherUsage(in []usage.NamedSummary) []daemon.OtherUsage {
	if len(in) == 0 {
		return nil
	}
	out := make([]daemon.OtherUsage, 0, len(in))
	for _, n := range in {
		out = append(out, daemon.OtherUsage{Agent: n.Agent, Usage: toUsageSummary(n.Summary)})
	}
	return out
}

// openRouterSpend fetches the configured OpenRouter account's spend.
// Returns a disabled (zero) value with no network call when no key is
// configured — the common case. A configured-but-failing fetch returns
// Enabled=true with ErrMsg set so the dashboard shows why the figure is
// missing rather than a silent blank.
func (s *server) openRouterSpend(ctx context.Context) daemon.OpenRouterSpend {
	cfg := s.cfg.OpenRouter
	if !cfg.Enabled || strings.TrimSpace(cfg.APIKey) == "" {
		return daemon.OpenRouterSpend{Enabled: false}
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	sp, err := openrouterusage.New(cfg.APIKey, cfg.BaseURL).Spend(ctx)
	if err != nil {
		return daemon.OpenRouterSpend{Enabled: true, ErrMsg: err.Error()}
	}
	return daemon.OpenRouterSpend{
		Enabled:    true,
		Usage:      sp.Usage,
		Limit:      sp.Limit,
		Remaining:  sp.Remaining,
		IsFreeTier: sp.IsFreeTier,
	}
}

func toUsageSummary(s usage.AgentSummary) daemon.UsageSummary {
	return daemon.UsageSummary{
		CachedInputTokens: s.CachedInputTokens,
		ReasoningTokens:   s.ReasoningTokens,
		CostAvailable:     s.CostAvailable,
		HasData:           s.HasData,
		WindowSeconds:     int(s.Window / time.Second),
		Prompts:           s.Prompts,
		InputTokens:       s.InputTokens,
		OutputTokens:      s.OutputTokens,
		EstimatedCost:     s.EstimatedCost,
	}
}
