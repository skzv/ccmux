package agentdetect

import (
	"testing"
)

// TestEvaluate_PicksHighestPriority — the headline engine contract.
// Two rules both match; the one with the higher Priority wins, full
// stop. Locks down so a reorder of the slice can't quietly change
// the outcome.
func TestEvaluate_PicksHighestPriority(t *testing.T) {
	rules := []Rule{
		{ID: "low", Priority: 100, Region: "whole_recent", State: "active", Contains: []string{"shared"}},
		{ID: "high", Priority: 900, Region: "whole_recent", State: "blocked", Contains: []string{"shared"}},
		{ID: "mid", Priority: 500, Region: "whole_recent", State: "idle", Contains: []string{"shared"}},
	}
	got := Evaluate(rules, Input{Pane: "this is a shared line"})
	if got.MatchedRuleID != "high" {
		t.Errorf("matched %q, want high (priority 900)", got.MatchedRuleID)
	}
	if got.State != StateNeedsInput {
		t.Errorf("state = %q, want blocked/needs_input", got.State)
	}
}

// TestEvaluate_NoMatchReturnsEmpty — the contract for callers: an
// unmatched input gives State=Unknown AND MatchedRuleID="", which is
// the signal to apply the legacy fallback.
func TestEvaluate_NoMatchReturnsEmpty(t *testing.T) {
	rules := []Rule{
		{ID: "only", Priority: 100, Region: "whole_recent", State: "active", Contains: []string{"nope"}},
	}
	got := Evaluate(rules, Input{Pane: "the pane"})
	if got.MatchedRuleID != "" {
		t.Errorf("MatchedRuleID = %q, want empty", got.MatchedRuleID)
	}
	if got.State != StateUnknown {
		t.Errorf("state = %q, want unknown", got.State)
	}
}

// TestEvaluate_TitleRegion — the OSC title is a separate region from
// the body. A rule scoped to osc_title must NOT match against the
// body, and vice-versa. Pin so a future region-extractor refactor
// can't quietly cross the streams.
func TestEvaluate_TitleRegion(t *testing.T) {
	rules := []Rule{{ID: "title-spin", Priority: 1000, Region: "osc_title", State: "working", Regex: []string{`^[\x{2800}-\x{28FF}]`}}}
	if Evaluate(rules, Input{Pane: "⠙ this is body"}).MatchedRuleID != "" {
		t.Error("title-scoped rule must not match against pane body")
	}
	res := Evaluate(rules, Input{Title: "⠙ building…"})
	if res.MatchedRuleID != "title-spin" || res.State != StateActive {
		t.Errorf("title rule didn't match braille spinner, got %+v", res)
	}
}

// TestExtractRegion_BottomNonEmptyLines — the most-used body region.
// Pins that empty lines are skipped (so footer detection ignores
// blank padding) and that the count is honored.
func TestExtractRegion_BottomNonEmptyLines(t *testing.T) {
	pane := "first\n\n\nsecond\n\nthird\n\n\n"
	in := Input{Pane: pane}
	got := extractRegion("bottom_non_empty_lines(2)", in)
	want := "second\nthird"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got = extractRegion("last_line", in)
	if got != "third" {
		t.Errorf("last_line = %q, want third", got)
	}
}

// TestExtractRegion_UnknownYieldsEmpty — a typo in a rule's `region`
// must return empty (which fails every match) rather than panic.
// Defensive: rule files are data, and a typo can ship.
func TestExtractRegion_UnknownYieldsEmpty(t *testing.T) {
	if got := extractRegion("not_a_real_region", Input{Pane: "x"}); got != "" {
		t.Errorf("unknown region = %q, want empty", got)
	}
}

// TestMatchSpec_ConjunctiveContains — listing two `contains` entries
// at the top level means BOTH must match. Easy to get wrong by reading
// it as `any`; pin it so the contract doesn't drift.
func TestMatchSpec_ConjunctiveContains(t *testing.T) {
	rules := []Rule{{ID: "both", Priority: 100, Region: "whole_recent", State: "blocked",
		Contains: []string{"alpha", "omega"}}}
	if Evaluate(rules, Input{Pane: "alpha only"}).MatchedRuleID != "" {
		t.Error("only one substring present — should NOT match (contains is conjunctive)")
	}
	if Evaluate(rules, Input{Pane: "alpha and omega"}).MatchedRuleID == "" {
		t.Error("both substrings present — should match")
	}
}

// TestMatchSpec_Not — `not` is negation: no sub-spec may match.
// Used in the Claude shell-prompt rule to avoid misfiring on the
// claude prompt frame.
func TestMatchSpec_Not(t *testing.T) {
	rules := []Rule{{ID: "shell-not-claude", Priority: 100, Region: "whole_recent", State: "error",
		Regex: []string{`\$\s*$`},
		Not:   []MatchSpec{{Contains: []string{"claude"}}}}}
	if Evaluate(rules, Input{Pane: "$ "}).MatchedRuleID == "" {
		t.Error("matched the dollar prompt, should fire error rule")
	}
	if Evaluate(rules, Input{Pane: "claude $ "}).MatchedRuleID != "" {
		t.Error("`not` clause should suppress match when 'claude' is in the region")
	}
}

// TestParseRegionArg — guards the small parser. Malformed values fall
// back to 1 rather than crashing or returning 0 (which would skip
// every line).
func TestParseRegionArg(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"bottom_non_empty_lines(3)", 3},
		{"bottom_non_empty_lines(0)", 1}, // 0 → 1 (the floor)
		{"bottom_non_empty_lines(x)", 1}, // garbage → 1
		{"bottom_non_empty_lines(", 1},   // unterminated → 1
		{"bottom_non_empty_lines()", 1},  // empty arg → 1
	} {
		if got := parseRegionArg(tc.in); got != tc.want {
			t.Errorf("parseRegionArg(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestRulesFor_LoadsBundledFiles — confirms the embed.FS hookup
// works end-to-end: every agent we ship a rules file for is reachable
// via RulesFor, and each rule list is non-empty.
func TestRulesFor_LoadsBundledFiles(t *testing.T) {
	for _, id := range []ID{
		"claude", "codex", "cursor", "pi", "grok", "antigravity",
		"opencode", "kimi", "droid", "copilot", "qoder", "kilo", "hermes", "amp", "kiro",
	} {
		got := RulesFor(id)
		if len(got) == 0 {
			t.Errorf("RulesFor(%q) returned no rules — embed.FS or TOML parsing broken", id)
		}
	}
	// A bogus ID returns empty so callers can detect "no rules for
	// this agent" cleanly.
	if got := RulesFor("ghost-agent"); len(got) != 0 {
		t.Errorf("RulesFor(unknown) = %d rules, want 0", len(got))
	}
}

// TestClassifyAgent_TitleSpinnerWins — sanity-checks the bundled rule
// for codex: a braille glyph in the title flips state to working,
// even when the body would otherwise look idle.
func TestClassifyAgent_TitleSpinnerWins(t *testing.T) {
	res, err := ClassifyAgent("codex", Input{Title: "⠙ building", Pane: "old idle output"})
	if err != nil {
		t.Fatalf("ClassifyAgent: %v", err)
	}
	if res.MatchedRuleID != "title_spinner_working" || res.State != StateActive {
		t.Errorf("title spinner expected to win, got %+v", res)
	}
}

// TestClassifyAgent_ActionRequired_CodexBlocked — codex's explicit
// blocker broadcast. Pins the title-rule end-to-end through the
// loader.
func TestClassifyAgent_ActionRequired_CodexBlocked(t *testing.T) {
	res, _ := ClassifyAgent("codex", Input{Title: "Action Required"})
	if res.MatchedRuleID != "title_action_required" {
		t.Errorf("matched %q, want title_action_required", res.MatchedRuleID)
	}
	if res.State != StateNeedsInput {
		t.Errorf("state = %q, want blocked/needs_input", res.State)
	}
}

// TestClassifyAgent_CodexConfirmPrompt — verifies the body footer
// rule fires for codex's "press enter to confirm" pattern.
func TestClassifyAgent_CodexConfirmPrompt(t *testing.T) {
	pane := "some output\n\nPress enter to confirm"
	res, _ := ClassifyAgent("codex", Input{Pane: pane})
	if res.MatchedRuleID != "body_confirm_prompt" {
		t.Errorf("matched %q, want body_confirm_prompt", res.MatchedRuleID)
	}
}

// TestClassifyAgent_NoRulesError — an unknown agent returns an error
// so a future caller can branch on "we have nothing for this agent."
func TestClassifyAgent_NoRulesError(t *testing.T) {
	_, err := ClassifyAgent("nonexistent-agent", Input{})
	if err == nil {
		t.Error("expected error for unknown agent")
	}
}

// TestClassifyAgent_SecondWaveDetection — exercises the bundled rule
// files for the second wave of agents end to end. Each row is a
// representative pane/title capture and the state its rules should
// produce. This is the guard that a typo in one of the new TOML files
// (bad region, mis-spelled operator) is caught instead of silently
// degrading that agent to the legacy quiet-pane fallback.
func TestClassifyAgent_SecondWaveDetection(t *testing.T) {
	cases := []struct {
		name  string
		agent ID
		in    Input
		want  State
	}{
		// Universal title spinner → working, for every new agent.
		{"opencode title spinner", "opencode", Input{Title: "⠹ building"}, StateActive},
		{"kimi title spinner", "kimi", Input{Title: "⠼ thinking"}, StateActive},
		{"droid title spinner", "droid", Input{Title: "⠧ working"}, StateActive},
		{"copilot title spinner", "copilot", Input{Title: "⠏ running"}, StateActive},
		{"qoder title spinner", "qoder", Input{Title: "⠛ working"}, StateActive},
		{"kilo title spinner", "kilo", Input{Title: "⠹ building"}, StateActive},
		{"hermes title spinner", "hermes", Input{Title: "⠶ working"}, StateActive},
		{"amp title spinner", "amp", Input{Title: "⠧ working"}, StateActive},
		{"kiro title spinner", "kiro", Input{Title: "⠏ working"}, StateActive},

		// Per-agent blocked prompts → needs_input.
		{"opencode permission", "opencode", Input{Pane: "some output\n△ Permission required\nallow this tool?"}, StateNeedsInput},
		{"kilo permission", "kilo", Input{Pane: "△ Permission required to run command"}, StateNeedsInput},
		{"droid approval", "droid", Input{Pane: "Run `rm -rf`?\n  yes, allow\n  no, cancel"}, StateNeedsInput},
		{"copilot confirm", "copilot", Input{Pane: "Apply this change?\nenter to confirm · esc to cancel"}, StateNeedsInput},
		{"qoder awaiting", "qoder", Input{Pane: "waiting for user confirmation to proceed"}, StateNeedsInput},
		{"amp approval", "amp", Input{Pane: "allow editing file: src/main.go?"}, StateNeedsInput},
		{"kiro approval", "kiro", Input{Pane: "This action requires approval.\n  yes, single permission"}, StateNeedsInput},
		{"hermes dangerous", "hermes", Input{Pane: "dangerous command detected\n  allow once\n  deny"}, StateNeedsInput},
		{"kimi approval", "kimi", Input{Pane: "Kimi wants to run command\n  ↵ confirm  ·  esc cancel"}, StateNeedsInput},

		// Per-agent working footers → active.
		{"opencode working", "opencode", Input{Pane: "generating code…\n(esc to interrupt)"}, StateActive},
		{"droid working", "droid", Input{Pane: "editing files…\nesc to stop"}, StateActive},
		{"qoder working", "qoder", Input{Pane: "thinking…\n(esc to cancel, ctrl+c to quit)"}, StateActive},
		{"kiro working", "kiro", Input{Pane: "kiro is working on your request"}, StateActive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ClassifyAgent(tc.agent, tc.in)
			if err != nil {
				t.Fatalf("ClassifyAgent(%q): %v", tc.agent, err)
			}
			if res.MatchedRuleID == "" {
				t.Fatalf("no rule matched for %q on %+v (would fall back to legacy heuristic)", tc.agent, tc.in)
			}
			if res.State != tc.want {
				t.Errorf("ClassifyAgent(%q) state = %q (rule %q), want %q",
					tc.agent, res.State, res.MatchedRuleID, tc.want)
			}
		})
	}
}
