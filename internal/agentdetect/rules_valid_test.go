package agentdetect

import (
	"fmt"
	"path"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BurntSushi/toml"
)

// TestBundledRuleFiles_Valid — loadCache skips a rule file that fails
// to parse and compileRegexList drops a regex that doesn't compile,
// both silently: a typo would quietly switch detection off for an
// agent. Check every bundled file strictly instead.
func TestBundledRuleFiles_Valid(t *testing.T) {
	entries, err := ruleFS.ReadDir("rules")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".toml") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			data, err := ruleFS.ReadFile(path.Join("rules", name))
			if err != nil {
				t.Fatal(err)
			}
			var rf ruleFile
			md, err := toml.Decode(string(data), &rf)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if u := md.Undecoded(); len(u) > 0 {
				t.Errorf("unknown keys (typo?): %v", u)
			}
			if want := strings.TrimSuffix(name, ".toml"); rf.ID != want {
				t.Errorf("id = %q, want %q (the file name)", rf.ID, want)
			}
			if len(rf.Rules) == 0 {
				t.Error("no rules")
			}
			seen := map[string]bool{}
			for _, r := range rf.Rules {
				if r.ID == "" {
					t.Error("rule with empty id")
				}
				if seen[r.ID] {
					t.Errorf("duplicate rule id %q", r.ID)
				}
				seen[r.ID] = true
				// Region, state, regexes and "has a positive
				// condition" — the loader drops a rule that fails.
				if err := validateRule(&r); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

// TestBundledRuleFiles_LoadWithoutErrors — loadCache can't surface a
// load error at runtime; it drops what it can't trust. So every shipped
// file must load with nothing dropped.
func TestBundledRuleFiles_LoadWithoutErrors(t *testing.T) {
	loaded, errs := loadRules(ruleFS)
	for _, err := range errs {
		t.Error(err)
	}
	if len(loaded) == 0 {
		t.Fatal("no rule files loaded")
	}
}

// TestValidateRule — a misspelled region used to extract an empty
// string and silently switch the rule off, and a rule whose only
// condition is `not` matched an empty region (a title-scoped one fired
// on every untitled session). Both, and the other silent degradations,
// are now load errors.
func TestValidateRule(t *testing.T) {
	re := func(s ...string) []string { return s }
	cases := []struct {
		name string
		rule Rule
		ok   bool
	}{
		{"whole pane", Rule{ID: "r", State: "working", Contains: re("x")}, true},
		{"explicit whole_recent", Rule{ID: "r", State: "working", Region: "whole_recent", Contains: re("x")}, true},
		{"last line", Rule{ID: "r", State: "blocked", Region: "last_line", Regex: re("x")}, true},
		{"osc title", Rule{ID: "r", State: "working", Region: "osc_title", Regex: re("x")}, true},
		{"osc progress", Rule{ID: "r", State: "working", Region: "osc_progress", Regex: re("x")}, true},
		{"bottom lines", Rule{ID: "r", State: "error", Region: "bottom_non_empty_lines(12)", Regex: re("x")}, true},
		{"not narrowing a regex", Rule{ID: "r", State: "error", Regex: re("x"), Not: []MatchSpec{{Regex: re("y")}}}, true},
		{"not narrowing an any", Rule{ID: "r", State: "error",
			Any: []MatchSpec{{Regex: re("a")}, {Contains: re("b")}}, Not: []MatchSpec{{Regex: re("y")}}}, true},
		{"not narrowing an all member", Rule{ID: "r", State: "error",
			All: []MatchSpec{{Not: []MatchSpec{{Regex: re("z")}}}, {LineRegex: re("a")}}, Not: []MatchSpec{{Regex: re("y")}}}, true},
		{"no conditions (non-empty fallback)", Rule{ID: "r", State: "idle"}, true},
		{"unknown state literal", Rule{ID: "r", State: "unknown", Contains: re("x")}, true},

		{"misspelled region", Rule{ID: "r", State: "error", Region: "bottom_non_emtpy_lines(4)", Regex: re("x")}, false},
		{"dash instead of underscore", Rule{ID: "r", State: "blocked", Region: "last-line", Regex: re("x")}, false},
		{"non-numeric line count", Rule{ID: "r", State: "error", Region: "bottom_non_empty_lines(x)", Regex: re("x")}, false},
		{"zero line count", Rule{ID: "r", State: "error", Region: "bottom_non_empty_lines(0)", Regex: re("x")}, false},
		{"unclosed line count", Rule{ID: "r", State: "error", Region: "bottom_non_empty_lines(4", Regex: re("x")}, false},
		{"not only", Rule{ID: "r", State: "error", Region: "osc_title", Not: []MatchSpec{{Contains: re("x")}}}, false},
		{"any with a negative-only branch", Rule{ID: "r", State: "error",
			Any: []MatchSpec{{Regex: re("a")}, {Not: []MatchSpec{{Regex: re("b")}}}}}, false},
		{"all of negatives", Rule{ID: "r", State: "error",
			All: []MatchSpec{{Not: []MatchSpec{{Regex: re("a")}}}}}, false},
		{"bad regex", Rule{ID: "r", State: "error", Regex: re("x"), Not: []MatchSpec{{Regex: re("(")}}}, false},
		{"unknown state", Rule{ID: "r", State: "blokced", Contains: re("x")}, false},
		{"empty id", Rule{State: "working", Contains: re("x")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRule(&tc.rule)
			if (err == nil) != tc.ok {
				t.Errorf("validateRule = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// TestLoadRules_DropsInvalidRules — the loader reports and skips a rule
// validateRule rejects instead of running it, and still loads the rest
// of that agent's file.
func TestLoadRules_DropsInvalidRules(t *testing.T) {
	fsys := fstest.MapFS{
		"rules/demo.toml": {Data: []byte(`id = "demo"

[[rules]]
id = "good"
state = "working"
priority = 800
region = "bottom_non_empty_lines(3)"
contains = ["esc to interrupt"]

[[rules]]
id = "typo_region"
state = "blocked"
priority = 900
region = "bottom_non_emtpy_lines(3)"
contains = ["Allow once"]

[[rules]]
id = "not_only"
state = "error"
priority = 1000
region = "osc_title"
not = [{ contains = ["demo"] }]
`)},
		"rules/broken.toml": {Data: []byte(`id = "broken"
[[rules]
`)},
	}
	loaded, errs := loadRules(fsys)
	var got []string
	for _, r := range loaded["demo"] {
		got = append(got, r.ID)
	}
	if strings.Join(got, ",") != "good" {
		t.Errorf("loaded demo rules = %v, want only [good]", got)
	}
	msgs := fmt.Sprint(errs)
	for _, want := range []string{"broken.toml", "typo_region", "not_only"} {
		if !strings.Contains(msgs, want) {
			t.Errorf("load errors %v don't mention %q", errs, want)
		}
	}
	// What the not-only rule did when it was loaded: fire on a session
	// that has no title at all, outranking every body rule.
	if res := Evaluate(loaded["demo"], Input{Pane: "(esc to interrupt)"}); res.MatchedRuleID != "good" {
		t.Errorf("Evaluate = %+v, want the good rule", res)
	}
}

// TestBundledRules_WholePaneBlockedRulesAreIdleGated — a blocked rule
// scanning the whole capture (all 60 lines, scrollback included) with
// no idle gate lets benign scrollback text ("requires approval",
// "Permission required", "invoke tool") pin needs_input and ring the
// bell instantly — at priority 900 it even beats the agent's own
// working footer while the agent is visibly busy. Such a rule must
// carry require_idle (and should really be scoped to the footer).
func TestBundledRules_WholePaneBlockedRulesAreIdleGated(t *testing.T) {
	entries, err := ruleFS.ReadDir("rules")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".toml") {
			continue
		}
		data, err := ruleFS.ReadFile(path.Join("rules", name))
		if err != nil {
			t.Fatal(err)
		}
		var rf ruleFile
		if _, err := toml.Decode(string(data), &rf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, r := range rf.Rules {
			if parseState(r.State) != StateNeedsInput || r.RequireIdle {
				continue
			}
			if region := strings.TrimSpace(r.Region); region == "whole_recent" || region == "" {
				t.Errorf("%s: blocked rule %q scans the whole pane (region %q) without require_idle",
					name, r.ID, r.Region)
			}
		}
	}
}
