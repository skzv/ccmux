package agentdetect

import (
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

//go:embed rules/*.toml
var ruleFS embed.FS

// ruleFile is the TOML schema for one agent's rules. The top-level
// fields are documented in the rule files themselves.
type ruleFile struct {
	ID    string `toml:"id"`
	Rules []Rule `toml:"rules"`
}

var (
	cacheOnce sync.Once
	cache     map[ID][]Rule
)

// RulesFor returns the compiled rule list for an agent. Empty (nil)
// when the agent has no rule file shipped, which is the caller's
// signal to apply its own legacy heuristic.
func RulesFor(id ID) []Rule {
	cacheOnce.Do(loadCache)
	return cache[id]
}

// loadCache reads every rule file from the embedded FS once and
// hands the result map to RulesFor. Concurrent reads after this are
// lock-free; the cache is read-only after Do.
func loadCache() {
	cache = map[ID][]Rule{}
	entries, err := ruleFS.ReadDir("rules")
	if err != nil {
		return
	}
	// Sort filenames so iteration is deterministic — useful in tests
	// and when emitting diagnostic output.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		data, err := ruleFS.ReadFile(filepath.Join("rules", name))
		if err != nil {
			continue
		}
		var rf ruleFile
		if _, err := toml.Decode(string(data), &rf); err != nil {
			continue
		}
		if rf.ID == "" {
			continue
		}
		id := ID(rf.ID)
		// Pre-compile every rule's match spec so the hot path doesn't
		// recompile on each poll tick.
		for i := range rf.Rules {
			r := &rf.Rules[i]
			spec := MatchSpec{
				Contains:  r.Contains,
				Regex:     r.Regex,
				LineRegex: r.LineRegex,
				Any:       r.Any,
				All:       r.All,
				Not:       r.Not,
			}
			spec.compile()
			// Stash the precompiled version back; ruleMatches
			// reconstructs the spec each call but uses the cached
			// compiled regexes on subsequent invocations.
			r.Match = spec
		}
		cache[id] = rf.Rules
	}
}

// ClassifyAgent is the high-level entry per-agent classifiers wrap.
// It evaluates the agent's embedded rule list against the input and
// returns the engine result. Callers translate Result to the final
// agent.State, typically applying their own time-based fallback when
// MatchedRuleID is empty.
//
// Returned with a string error context only on a genuinely missing
// agent — present here so future callers can branch on "no rules at
// all" without re-checking RulesFor themselves.
func ClassifyAgent(id ID, in Input) (Result, error) {
	rules := RulesFor(id)
	if len(rules) == 0 {
		return Result{State: StateUnknown}, fmt.Errorf("agentdetect: no rules for %q", id)
	}
	return Evaluate(rules, in), nil
}
