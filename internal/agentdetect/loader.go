package agentdetect

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
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
// lock-free; the cache is read-only after Do. Load errors can't be
// returned from here; the bundled files are held to zero errors by
// TestBundledRuleFiles_LoadWithoutErrors instead.
func loadCache() {
	cache, _ = loadRules(ruleFS)
}

// loadRules reads every rules/*.toml file in fsys and returns the
// compiled rules per agent, plus one error for everything it had to
// leave out: a file that doesn't parse or has no id, and each rule
// validateRule rejects. A rejected rule is dropped rather than run —
// it could only misfire — while the rest of its file still loads.
func loadRules(fsys fs.FS) (map[ID][]Rule, []error) {
	out := map[ID][]Rule{}
	var errs []error
	entries, err := fs.ReadDir(fsys, "rules")
	if err != nil {
		return out, []error{fmt.Errorf("agentdetect: read rules dir: %w", err)}
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
		data, err := fs.ReadFile(fsys, path.Join("rules", name)) // fs.FS paths are always slash-separated, even on Windows
		if err != nil {
			errs = append(errs, fmt.Errorf("agentdetect: %s: %w", name, err))
			continue
		}
		var rf ruleFile
		if _, err := toml.Decode(string(data), &rf); err != nil {
			errs = append(errs, fmt.Errorf("agentdetect: %s: %w", name, err))
			continue
		}
		if rf.ID == "" {
			errs = append(errs, fmt.Errorf("agentdetect: %s: missing top-level id", name))
			continue
		}
		// Pre-compile every rule's match spec — including the nested
		// Any/All/Not tree — so the hot path never compiles a regex
		// and never mutates shared rule state (classification runs
		// concurrently across sessions). ruleMatches uses r.Match
		// directly when it's compiled.
		rules := make([]Rule, 0, len(rf.Rules))
		for i := range rf.Rules {
			r := rf.Rules[i]
			if err := validateRule(&r); err != nil {
				errs = append(errs, fmt.Errorf("agentdetect: %s: %w", name, err))
				continue
			}
			spec := r.spec()
			spec.compile()
			r.Match = spec
			rules = append(rules, r)
		}
		out[ID(rf.ID)] = rules
	}
	return out, errs
}

// spec gathers a rule's flattened top-level conditions into a MatchSpec.
func (r *Rule) spec() MatchSpec {
	return MatchSpec{
		Contains:  r.Contains,
		Regex:     r.Regex,
		LineRegex: r.LineRegex,
		Any:       r.Any,
		All:       r.All,
		Not:       r.Not,
	}
}

// validateRule reports why a rule can't be trusted to mean what it
// says. Each of these used to degrade silently at runtime:
//
//   - An unknown region (a typo such as `bottom_non_emtpy_lines(4)`)
//     extracts "", so the rule never fires — detection for that shape
//     just switches off.
//   - An unknown state classifies a match as "unknown".
//   - A regex that doesn't compile was dropped from its spec, widening
//     the rule: drop the only positive regex from a rule that also has
//     a `not` and it matches nearly every pane.
//   - A rule whose conditions are all negative (`not` alone, or an
//     `any` with a purely negative branch) matches whatever lacks the
//     excluded text — including an empty region, so a title-scoped
//     rule fires on every session with no title. A `not` has to
//     narrow a positive condition (`contains` / `regex` / `line_regex`).
//     A rule with no conditions at all is still allowed: it is the
//     documented "any non-empty region" fallback.
func validateRule(r *Rule) error {
	if r.ID == "" {
		return errors.New("rule with empty id")
	}
	if !validRegion(r.Region) {
		return fmt.Errorf("rule %q: unknown region %q", r.ID, r.Region)
	}
	if st := parseState(r.State); st == StateUnknown && strings.ToLower(strings.TrimSpace(r.State)) != "unknown" {
		return fmt.Errorf("rule %q: unknown state %q", r.ID, r.State)
	}
	spec := r.spec()
	if err := checkSpecRegexes(&spec); err != nil {
		return fmt.Errorf("rule %q: %w", r.ID, err)
	}
	if !spec.empty() && !spec.hasPositive() {
		return fmt.Errorf("rule %q: every condition is negative (`not` needs a contains/regex/line_regex to narrow), so it matches any pane without the excluded text, even an empty one", r.ID)
	}
	return nil
}

// checkSpecRegexes compiles every regex in the spec tree, reporting the
// first that fails (compileRegexList would silently drop it).
func checkSpecRegexes(m *MatchSpec) error {
	for _, src := range append(append([]string{}, m.Regex...), m.LineRegex...) {
		if _, err := regexp.Compile(src); err != nil {
			return fmt.Errorf("regex %q: %w", src, err)
		}
	}
	for _, group := range [][]MatchSpec{m.Any, m.All, m.Not} {
		for i := range group {
			if err := checkSpecRegexes(&group[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// empty reports whether the spec sets no condition at all.
func (m *MatchSpec) empty() bool {
	return len(m.Contains) == 0 && len(m.Regex) == 0 && len(m.LineRegex) == 0 &&
		len(m.Any) == 0 && len(m.All) == 0 && len(m.Not) == 0
}

// hasPositive reports whether the spec can only match when some
// positive condition (contains / regex / line_regex) holds: one of its
// own, one inside an `all` member, or one in EVERY `any` branch — a
// single purely negative branch lets the whole disjunction match on
// negation alone.
func (m *MatchSpec) hasPositive() bool {
	if len(m.Contains) > 0 || len(m.Regex) > 0 || len(m.LineRegex) > 0 {
		return true
	}
	for i := range m.All {
		if m.All[i].hasPositive() {
			return true
		}
	}
	if len(m.Any) == 0 {
		return false
	}
	for i := range m.Any {
		if !m.Any[i].hasPositive() {
			return false
		}
	}
	return true
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
