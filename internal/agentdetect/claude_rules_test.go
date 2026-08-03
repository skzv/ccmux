package agentdetect

import (
	"sync"
	"testing"
)

// TestClaudeRules_PromptFrameRequiresTwoDistinctGlyphs — finding: the
// old claude_prompt_frame encoding let one rounded glyph satisfy both
// the top-level regex AND the `any` block, so a capture racing a
// partial frame redraw (a lone `╰` on the last line) classified
// blocked and fired a spurious bell/push. The rule must mirror
// looksLikeClaudePrompt's contract: one rounded corner AND a second
// DISTINCT glyph from {╭╮╰╯│─>}.
func TestClaudeRules_PromptFrameRequiresTwoDistinctGlyphs(t *testing.T) {
	rules := RulesFor("claude")
	if len(rules) == 0 {
		t.Fatal("no rules loaded for claude")
	}
	cases := []struct {
		name      string
		pane      string
		wantMatch bool
	}{
		{"lone rounded corner (partial redraw)", "output\n╰", false},
		{"same rounded glyph repeated", "output\n╰╰", false},
		{"real frame bottom", "output\n╰──────────╯", true},
		{"corner plus caret", "output\n╭ >", true},
		{"two distinct rounded corners", "output\n╰╯", true},
		{"frame glyphs but no rounded corner", "output\n│ plain box │", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Evaluate(rules, Input{Pane: tc.pane})
			matched := res.MatchedRuleID == "claude_prompt_frame"
			if matched != tc.wantMatch {
				t.Errorf("pane %q: claude_prompt_frame matched=%v, want %v (matched rule %q)",
					tc.pane, matched, tc.wantMatch, res.MatchedRuleID)
			}
			if matched && !res.RequireIdle {
				t.Error("claude_prompt_frame must carry RequireIdle so the caller re-applies the idle gate")
			}
		})
	}
}

// TestEvaluate_PropagatesRequireIdle — the engine must hand the
// rule-level require_idle flag to the caller (which owns the idle
// tracking) on the winning rule's Result.
func TestEvaluate_PropagatesRequireIdle(t *testing.T) {
	rules := []Rule{{ID: "r", Priority: 1, State: "blocked", Region: "whole_recent",
		Contains: []string{"x"}, RequireIdle: true}}
	res := Evaluate(rules, Input{Pane: "x"})
	if res.MatchedRuleID != "r" {
		t.Fatalf("rule did not match: %+v", res)
	}
	if !res.RequireIdle {
		t.Error("RequireIdle not propagated to Result")
	}
}

// TestLoadCache_PrecompilesNestedSpecs — finding 6: the loader only
// compiled top-level Regex/LineRegex; nested Any/All/Not specs were
// lazily compiled during classification by mutating shared slice
// elements (a latent data race). The whole tree must be compiled at
// load time.
func TestLoadCache_PrecompilesNestedSpecs(t *testing.T) {
	rules := RulesFor("claude")
	var frame *Rule
	for i := range rules {
		if rules[i].ID == "claude_prompt_frame" {
			frame = &rules[i]
		}
	}
	if frame == nil {
		t.Fatal("claude_prompt_frame rule not found")
	}
	var walk func(t *testing.T, spec *MatchSpec, path string)
	walk = func(t *testing.T, spec *MatchSpec, path string) {
		if !spec.hasCompiled {
			t.Errorf("%s: not compiled at load time", path)
		}
		if len(spec.compiled) != len(spec.Regex) {
			t.Errorf("%s: %d compiled regexes for %d sources", path, len(spec.compiled), len(spec.Regex))
		}
		for i := range spec.Any {
			walk(t, &spec.Any[i], path+".Any")
		}
		for i := range spec.All {
			walk(t, &spec.All[i], path+".All")
		}
		for i := range spec.Not {
			walk(t, &spec.Not[i], path+".Not")
		}
	}
	if len(frame.Match.Any) == 0 {
		t.Fatal("expected nested Any specs on claude_prompt_frame")
	}
	walk(t, &frame.Match, "Match")
}

// TestClassifyAgent_ConcurrentIsRaceFree — run under -race. Before the
// fix, concurrent classification lazily compiled the nested Any/All/Not
// specs by writing to shared slice elements; two sessions classifying
// on the same tick raced. Classification must be read-only.
func TestClassifyAgent_ConcurrentIsRaceFree(t *testing.T) {
	inputs := []Input{
		{Pane: "x\n╰──────╯"},
		{Pane: "done\n$ "},
		{Title: "⠙ spinning"},
		{Pane: "plain output"},
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = ClassifyAgent("claude", inputs[(g+i)%len(inputs)])
				_, _ = ClassifyAgent("codex", inputs[(g+i)%len(inputs)])
			}
		}(g)
	}
	wg.Wait()
}
