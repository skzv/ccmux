package agentdetect

import (
	"path"
	"regexp"
	"strings"
	"testing"

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
				if parseState(r.State) == StateUnknown && strings.TrimSpace(strings.ToLower(r.State)) != "unknown" {
					t.Errorf("rule %q: unrecognised state %q", r.ID, r.State)
				}
				checkRegexes(t, r.ID, MatchSpec{Regex: r.Regex, LineRegex: r.LineRegex, Any: r.Any, All: r.All, Not: r.Not})
			}
		})
	}
}

func checkRegexes(t *testing.T, ruleID string, m MatchSpec) {
	t.Helper()
	for _, src := range append(append([]string{}, m.Regex...), m.LineRegex...) {
		if _, err := regexp.Compile(src); err != nil {
			t.Errorf("rule %q: regex %q does not compile: %v", ruleID, src, err)
		}
	}
	for _, group := range [][]MatchSpec{m.Any, m.All, m.Not} {
		for _, sub := range group {
			checkRegexes(t, ruleID, sub)
		}
	}
}
