// Package usage is the per-agent token-usage walker layer:
//
//   - Claude: delegates to internal/claudeusage.WalkRolling (the rich
//     walker). The dashboard's main Claude panel reads the 5h session
//     block for its quota bar + per-project drill-down through
//     WalkClaudeBlock, the same walk the daemon reports as claude_block.
//   - Codex: internal/codexusage. Antigravity, Gemini, Muse: their own
//     walkers below. The remaining agents go through the generic
//     JSONL walker in internal/agentusage (WalkOthers).
//
// The dashboard uses this package to render compact per-agent rows
// beneath the Claude panel so users adopting Codex / Antigravity see
// at a glance whether ccmux has discovered their transcripts yet.
// Empty rows still get rendered with an "install hint" so the
// first-time user knows what to do.
//
// Why a separate type from claudeusage.Aggregate: Claude's
// transcript shape carries cache-create/cache-read tokens and
// session-block quota semantics that don't map to Codex/Antigravity's
// pricing model. AgentSummary keeps only the fields that mean the
// same thing across every agent: prompts, input/output tokens, and
// an API-rates cost estimate.
package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/agenthome"
	"github.com/skzv/ccmux/internal/agentusage"
	"github.com/skzv/ccmux/internal/claudeusage"
	"github.com/skzv/ccmux/internal/codexusage"
	"github.com/skzv/ccmux/internal/gemini"
	"github.com/skzv/ccmux/internal/jsonl"
	"github.com/skzv/ccmux/internal/muse"
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

	CachedInputTokens int
	ReasoningTokens   int
	CostAvailable     *bool
	Window            time.Duration
	Prompts           int     // user-initiated turns in the window
	InputTokens       int     // billed input tokens
	OutputTokens      int     // billed output tokens
	EstimatedCost     float64 // USD at the agent's published API rates
}

// TotalTokens returns the input+output sum. Cache tokens (Claude-
// specific) are accounted in the rich Claude panel separately.
func (s AgentSummary) TotalTokens() int {
	return s.InputTokens + s.OutputTokens
}

// NamedSummary pairs an agent ID with its usage summary, for the
// dashboard's dynamic per-agent rows (only agents with actual usage are
// returned by WalkOthers, so the panel sizes to what you use).
type NamedSummary struct {
	Agent   string
	Summary AgentSummary
}

// genericWalkAgents are the second-wave agents whose usage we read with
// the format-agnostic JSONL walker (internal/agentusage) rather than a
// bespoke parser. Claude / Codex have their own rich Walk* above;
// Antigravity's transcripts are opaque protobuf (handled separately).
var genericWalkAgents = []agent.ID{
	agent.IDOpenCode, agent.IDKimi, agent.IDDroid, agent.IDCopilot,
	agent.IDQoder, agent.IDKilo, agent.IDHermes, agent.IDAmp, agent.IDKiro,
}

// WalkOthers runs the generic JSONL walker over every second-wave
// agent's transcript root and returns summaries only for the agents
// that actually have usage in the window. Agents with no recognizable
// data are omitted entirely (not returned as empty rows), so the
// dashboard shows a row only for an agent you actually use — the roster
// is large enough now that rendering 9 "no conversations yet"
// placeholders would be noise.
func WalkOthers(window time.Duration) []NamedSummary {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []NamedSummary
	if s := WalkGemini(home, window); s.HasData {
		out = append(out, NamedSummary{Agent: "gemini", Summary: s})
	}
	if s := WalkMuse(home, window); s.HasData {
		out = append(out, NamedSummary{Agent: "muse", Summary: s})
	}
	for _, id := range genericWalkAgents {
		root := agent.ByID(id).TranscriptsRoot(home)
		s, err := agentusage.Walk(root, window)
		if err != nil || !s.HasData {
			continue
		}
		out = append(out, NamedSummary{
			Agent: string(id),
			Summary: AgentSummary{
				HasData:      true,
				Window:       window,
				Prompts:      s.Prompts,
				InputTokens:  s.InputTokens,
				OutputTokens: s.OutputTokens,
			},
		})
	}
	return out
}

// WalkClaude returns the cross-agent summary for Claude Code by
// delegating to the claudeusage walker. Like every other agent's
// summary it covers the plain rolling window [now-window, now]
// (claudeusage.WalkRolling); the rich Claude panel in the dashboard
// reads the subscription session block through claudeusage.Walk.
func WalkClaude(window time.Duration) (AgentSummary, error) {
	agg, err := claudeusage.WalkRolling(window)
	if err != nil || agg == nil {
		return AgentSummary{}, err
	}
	// HasData reflects whether we actually FOUND usage, not whether
	// the walker ran without error. An empty ~/.claude/projects/
	// (fresh install, never used Claude) returns a valid-but-zero
	// Aggregate; we want the dashboard to show the install hint for
	// that case, same as for Codex/Antigravity stubs. Messages > 0 is
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

// WalkCodex aggregates per-window usage from ~/.codex/sessions/
// rollout files via the codexusage package. The cross-agent
// AgentSummary loses Codex's cached-input breakdown (it's already
// folded into Input there) — that's fine for the compact dashboard
// row, which just shows total tokens + best-effort cost.
//
// Returns HasData=false when ccmux finds no rollout files or every
// file's events fall outside the window. The dashboard renders that
// case with the install-hint placeholder so a fresh-install user
// gets a copy-pasteable command instead of a confusing "0 tokens"
// row that looks like ccmux is broken.
func WalkCodex(window time.Duration) (AgentSummary, error) {
	agg, err := codexusage.Walk(window)
	if err != nil || agg == nil {
		return AgentSummary{}, err
	}
	if agg.Messages == 0 && agg.UserPrompts == 0 {
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

type antigravityTranscriptStep struct {
	Source          string `json:"source"`
	Type            string `json:"type"`
	CreatedAt       string `json:"created_at"`
	InputTokens     int    `json:"input_tokens"`
	CacheReadTokens int    `json:"cache_read_tokens"`
	OutputTokens    int    `json:"output_tokens"`
}

// WalkAntigravity aggregates usage from Antigravity CLI's plain JSONL
// transcripts under ~/.gemini/antigravity-cli/brain/<uuid>/.system_generated/logs/transcript.jsonl
// (summing real prompts, input, cache-read, and output tokens within
// window). When no JSONL transcripts exist in the window (older
// installs or .pb-only fixtures), it falls back to counting recent
// ~/.gemini/antigravity-cli/conversations/<uuid>.pb files by mtime so
// the dashboard still surfaces activity honestly.
func WalkAntigravity(window time.Duration) (AgentSummary, error) {
	if window <= 0 {
		window = 24 * time.Hour
	}
	home, err := os.UserHomeDir()
	base := ""
	if dir, ok := agenthome.Override(agenthome.AntigravityEnv, home); ok {
		base = dir
	} else {
		if err != nil {
			return AgentSummary{}, nil //nolint:nilerr // missing $HOME → empty summary, not an error to surface
		}
		base = agenthome.Antigravity(home)
	}
	cutoff := time.Now().Add(-window)

	// 1. Prefer plain JSONL transcripts under brain/<uuid>/.system_generated/logs/.
	if sum, ok := walkAntigravityBrainJSONL(filepath.Join(base, "brain"), window, cutoff); ok {
		return sum, nil
	}

	// 2. Fallback: count .pb files touched within the window.
	root := filepath.Join(base, "conversations")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return AgentSummary{}, nil
		}
		return AgentSummary{}, err
	}
	prompts := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pb") {
			continue
		}
		info, err := e.Info()
		if e.Type()&os.ModeSymlink != 0 {
			if si, serr := os.Stat(filepath.Join(root, e.Name())); serr == nil {
				info, err = si, nil
			}
		}
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			prompts++
		}
	}
	if prompts == 0 {
		return AgentSummary{}, nil
	}
	return AgentSummary{
		HasData:      true,
		Window:       window,
		Prompts:      prompts,
		InputTokens:  0, // unknown — opaque protobuf fallback
		OutputTokens: 0,
	}, nil
}

func walkAntigravityBrainJSONL(brainRoot string, window time.Duration, cutoff time.Time) (AgentSummary, bool) {
	entries, err := os.ReadDir(brainRoot)
	if err != nil {
		return AgentSummary{}, false
	}
	var sum AgentSummary
	sum.Window = window
	found := false
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		tPath := filepath.Join(brainRoot, e.Name(), ".system_generated", "logs", "transcript.jsonl")
		info, err := os.Stat(tPath)
		if err != nil || info.IsDir() || !info.ModTime().After(cutoff) {
			continue
		}
		f, err := os.Open(tPath)
		if err != nil {
			continue
		}
		sc := jsonl.NewScanner(f, 4*1024*1024)
		for sc.Scan() {
			var step antigravityTranscriptStep
			if err := json.Unmarshal(sc.Bytes(), &step); err != nil {
				continue
			}
			ts := info.ModTime()
			if step.CreatedAt != "" {
				if parsed, err := time.Parse(time.RFC3339Nano, step.CreatedAt); err == nil {
					ts = parsed
				} else if parsed, err := time.Parse(time.RFC3339, step.CreatedAt); err == nil {
					ts = parsed
				}
			}
			if ts.Before(cutoff) {
				continue
			}
			if step.Type == "USER_INPUT" || step.Source == "USER_EXPLICIT" {
				sum.Prompts++
				found = true
			}
			if step.InputTokens > 0 || step.CacheReadTokens > 0 || step.OutputTokens > 0 {
				sum.InputTokens += step.InputTokens
				sum.CachedInputTokens += step.CacheReadTokens
				sum.OutputTokens += step.OutputTokens
				found = true
			}
		}
		_ = f.Close()
	}
	if !found {
		return AgentSummary{}, false
	}
	sum.HasData = true
	return sum, true
}

// WalkMuse uses completion events, never duplicate attribution samples.
func WalkMuse(home string, window time.Duration) AgentSummary {
	unavailable := false
	out := AgentSummary{Window: window, CostAvailable: &unavailable}
	sessions, err := muse.List(home)
	if err != nil {
		return out
	}
	cutoff := time.Now().Add(-window)
	for _, s := range sessions {
		for _, m := range s.Messages {
			if m.Role == "user" && (window <= 0 || !m.Time.Before(cutoff)) {
				out.Prompts++
				out.HasData = true
			}
		}
		for _, r := range s.Requests {
			if window > 0 && r.Time.Before(cutoff) {
				continue
			}
			out.HasData = true
			out.InputTokens += r.Input
			out.OutputTokens += r.Output
			out.CachedInputTokens += r.Cached
			out.ReasoningTokens += r.Reasoning
		}
	}
	return out
}

// WalkGemini totals native per-message counters after JSONL updates and
// rewinds have been applied. Subscription/API costs cannot be inferred here.
// A subagent session's "user" messages are the parent agent's
// instructions, not prompts; its tokens are still real usage.
func WalkGemini(home string, window time.Duration) AgentSummary {
	unavailable := false
	out := AgentSummary{Window: window, CostAvailable: &unavailable}
	sessions, err := gemini.List(home)
	if err != nil {
		return out
	}
	cutoff := time.Now().Add(-window)
	for _, s := range sessions {
		subagent := s.Kind == "subagent"
		for _, m := range s.Messages {
			ts, err := time.Parse(time.RFC3339Nano, m.Timestamp)
			if window > 0 && (err != nil || ts.Before(cutoff)) {
				continue
			}
			if m.Type == "user" && !subagent {
				out.Prompts++
				out.HasData = true
			}
			if m.Type == "gemini" && m.Tokens != nil {
				out.HasData = true
				out.InputTokens += m.Tokens.Input
				out.OutputTokens += m.Tokens.Output
				out.CachedInputTokens += m.Tokens.Cached
				out.ReasoningTokens += m.Tokens.Thoughts
			}
		}
	}
	return out
}
