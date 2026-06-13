package agent

import (
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/agentdetect"
	"github.com/skzv/ccmux/internal/claude"
)

// engineClassify runs the data-driven detection engine for an agent
// and falls back to the documented legacy time-based heuristic when
// no rule matches. The fallback preserves the pre-Phase-3 behavior
// of every classifier whose rule file doesn't yet cover their full
// pane surface, so adding a new rule sharpens detection without ever
// regressing a known case.
//
// The fallback rules:
//
//   - Empty pane → Unknown. There's no signal to classify yet.
//   - Idle past the threshold → NeedsInput. Same coarse "went quiet"
//     rule the bare classifiers used; conservative but never wrong
//     in the most common case (user typed, agent answered, pane
//     went quiet — they're waiting for the next prompt).
//   - Otherwise → Active. Pane has content and changed recently.
//
// title is the OSC-set pane title passed through from the poll loop
// (see Phase 1). It's part of the engine input alongside the body so
// rule files can match on either signal.
func engineClassify(id ID, pane, title string, lastChange time.Time, idleThreshold time.Duration) State {
	res, _ := agentdetect.ClassifyAgent(agentdetect.ID(id), agentdetect.Input{Pane: pane, Title: title})
	if res.MatchedRuleID != "" && !res.SkipStateUpdate {
		return State(res.State)
	}
	// Per-agent fallback. Claude has its own well-tuned body
	// classifier in internal/claude — fall through to that so the
	// existing fixture-pinned behavior survives any rule miss. Every
	// other agent uses the documented time-based heuristic.
	if id == IDClaude {
		return State(claude.Classify(pane, lastChange, idleThreshold))
	}
	return legacyFallback(pane, lastChange, idleThreshold)
}

// legacyFallback is the pre-Phase-3 "went quiet = needs_input"
// heuristic, kept here so a rule miss never silently turns into
// StateUnknown. Inlined into each agent's old classifier; consolidated
// here so adding an agent doesn't copy-paste it again.
func legacyFallback(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	if strings.TrimSpace(pane) == "" {
		return StateUnknown
	}
	if time.Since(lastChange) >= idleThreshold {
		return StateNeedsInput
	}
	return StateActive
}
