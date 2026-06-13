// Package agentdetect is the declarative detection engine that
// classifies an agent CLI's session state from its pane body and OSC
// title. Each agent ships a small set of priority-ordered, region-
// scoped rules; the engine evaluates them, the highest-priority match
// wins. Rules are data, not code — a new agent or a sharper rule for
// an existing one is a TOML edit, not a code change.
//
// Why this exists. The previous classifiers were per-agent Go heuristics:
// Claude had a tuned set of rules around its box-drawing prompt frame,
// and every other agent (Codex, Cursor, Pi, Grok, Antigravity) was a
// "v1 best-effort stub" — pane went quiet → needs_input — because we
// had no good per-agent body shape to look for. That coarse rule
// produced spurious bells and missed real ones.
//
// The shape this engine encodes mirrors what production-grade agent
// supervisors converge on:
//
//   - Region: WHERE in the captured pane state to look — the whole
//     recent body, the last N non-empty lines, the OSC-set title (a
//     second high-quality signal added in the Phase 1 PR), the OSC
//     progress channel.
//   - Match: WHAT to look for — substring `contains`, anchored `regex`,
//     plus boolean composition via `any`/`all`/`not`. Conditions are
//     conjunctive at the rule level: every constraint listed on a rule
//     must hold for the rule to match.
//   - Priority + Skip: WHICH rule wins. A high-priority "transcript
//     viewer" rule with `skip_state_update=true` parks the classifier
//     on the previous state when the user is just paging history, so
//     viewing scrollback doesn't trigger spurious state churn.
//
// The fallback. A pane with NO matching rule falls back to the
// engine-level default state (Unknown) — but per-agent classifiers
// (`internal/agent/<id>.go`) wrap this engine and apply a time-based
// safety net for agents whose rule files don't yet cover every case.
// That keeps backward compatibility while the rule files grow.
package agentdetect

import (
	"regexp"
	"strings"
)

// State mirrors internal/agent.State as strings, redeclared here so
// the engine doesn't import internal/agent (the per-agent classifiers
// wrap this engine, which would otherwise create an import cycle).
// String values match agent.State verbatim; the caller casts at the
// seam (see internal/agent/engine.go).
type State string

const (
	StateUnknown    State = "unknown"
	StateActive     State = "active"
	StateIdle       State = "idle"
	StateNeedsInput State = "needs_input"
	StateError      State = "error"
)

// ID mirrors agent.ID for the same reason. Values match exactly.
type ID string

// Input is the captured detection surface for one session at one
// poll tick. The OSC title typically carries the most reliable
// signal (agents broadcast working spinners and explicit blocker
// strings there) while the pane body covers everything else.
type Input struct {
	Pane     string // captured pane content; last N lines, ANSI-stripped if applicable
	Title    string // OSC-set title (#{pane_title})
	Progress string // OSC progress (#{pane_progress}); reserved for future agents
}

// MatchSpec is the set of conditions a rule places on its target region.
// The semantics are conjunctive — every condition that's set must hold.
// An unset/empty field is "don't care". Use Any/All/Not to compose
// non-conjunctive logic out of the atomic conditions.
type MatchSpec struct {
	Contains  []string    `toml:"contains"`
	Regex     []string    `toml:"regex"`
	LineRegex []string    `toml:"line_regex"`
	Any       []MatchSpec `toml:"any"`
	All       []MatchSpec `toml:"all"`
	Not       []MatchSpec `toml:"not"`

	// Compiled regexes are cached on first evaluation. Tests construct
	// MatchSpec values directly without compilation; production loads
	// from TOML and calls compile() once.
	compiled    []*regexp.Regexp
	compiledLn  []*regexp.Regexp
	hasCompiled bool
}

// Rule is one row in an agent's rule file. The highest-priority
// matching rule across all rules for an agent wins; ties between equal
// priority are deterministic on insertion order.
type Rule struct {
	ID        string      `toml:"id"`
	Priority  int         `toml:"priority"`
	Region    string      `toml:"region"`
	State     string      `toml:"state"` // "working" | "blocked" | "idle" | "unknown"
	Match     MatchSpec   `toml:"-"`     // flattened into Rule via TOML; see decoding
	Contains  []string    `toml:"contains"`
	Regex     []string    `toml:"regex"`
	LineRegex []string    `toml:"line_regex"`
	Any       []MatchSpec `toml:"any"`
	All       []MatchSpec `toml:"all"`
	Not       []MatchSpec `toml:"not"`

	// SkipStateUpdate parks the classifier on the previous state when
	// the rule matches. Used for transient overlays (transcript
	// viewer, help screens) so paging through history doesn't oscillate
	// the state.
	SkipStateUpdate bool `toml:"skip_state_update"`
}

// Result is what the engine returns. State is the new derived state;
// SkipStateUpdate signals to the caller "keep the previous state."
// MatchedRuleID is exposed for logging/debugging; an empty value means
// "no rule matched."
type Result struct {
	State           State
	MatchedRuleID   string
	SkipStateUpdate bool
}

// Evaluate runs the rule list against an Input and returns the
// highest-priority match. Rules are scanned in order, so ties between
// equal priorities are resolved by appearance — keep `priority` as
// the primary expression of intent and rely on appearance only as
// a stable tiebreaker.
//
// An empty result (State=Unknown, MatchedRuleID="") means no rule
// matched and the caller should apply its own fallback.
func Evaluate(rules []Rule, in Input) Result {
	best := -1
	var bestRule *Rule
	for i := range rules {
		r := &rules[i]
		if !ruleMatches(r, in) {
			continue
		}
		if r.Priority > best {
			best = r.Priority
			bestRule = r
		}
	}
	if bestRule == nil {
		return Result{State: StateUnknown}
	}
	return Result{
		State:           parseState(bestRule.State),
		MatchedRuleID:   bestRule.ID,
		SkipStateUpdate: bestRule.SkipStateUpdate,
	}
}

// ruleMatches is the per-rule predicate: select the region, run the
// flattened MatchSpec, return true on match.
func ruleMatches(r *Rule, in Input) bool {
	region := extractRegion(r.Region, in)
	spec := MatchSpec{
		Contains:  r.Contains,
		Regex:     r.Regex,
		LineRegex: r.LineRegex,
		Any:       r.Any,
		All:       r.All,
		Not:       r.Not,
	}
	return spec.match(region)
}

// extractRegion pulls the substring of the input that the rule wants
// to scrutinize. Unknown region names return an empty string, which
// fails every match — so a typo in a rule file makes the rule a
// no-op rather than a panic.
func extractRegion(name string, in Input) string {
	switch {
	case name == "osc_title":
		return in.Title
	case name == "osc_progress":
		return in.Progress
	case name == "whole_recent", name == "":
		return in.Pane
	case strings.HasPrefix(name, "bottom_non_empty_lines("):
		return lastNonEmptyLines(in.Pane, parseRegionArg(name))
	case name == "last_line":
		return lastNonEmptyLines(in.Pane, 1)
	}
	return ""
}

// parseRegionArg returns the N inside a `name(N)` region string, with
// a sane fallback so a malformed value can't crash classification.
func parseRegionArg(name string) int {
	open := strings.Index(name, "(")
	close := strings.Index(name, ")")
	if open < 0 || close < 0 || close <= open {
		return 1
	}
	n := 0
	for _, ch := range name[open+1 : close] {
		if ch < '0' || ch > '9' {
			return 1
		}
		n = n*10 + int(ch-'0')
	}
	if n <= 0 {
		return 1
	}
	return n
}

// lastNonEmptyLines returns the last `n` non-empty lines of s, joined
// by newline. Used for `bottom_non_empty_lines(N)` and `last_line`
// regions — those see the actively-rendered footer of the agent's
// TUI, which is where prompt frames and "press enter" hints live.
func lastNonEmptyLines(s string, n int) string {
	if s == "" || n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, n)
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		out = append([]string{lines[i]}, out...)
	}
	return strings.Join(out, "\n")
}

// match evaluates the MatchSpec against a region. All set conditions
// must hold (conjunctive). An empty MatchSpec matches any non-empty
// region — useful for "fallback" rules that just want to fire on the
// presence of content.
func (m *MatchSpec) match(region string) bool {
	if !m.hasCompiled {
		m.compile()
	}
	// Empty region with no positive conditions to evaluate is a
	// non-match — we don't want an "empty body matches everything"
	// catch-all.
	if region == "" && len(m.Contains) == 0 && len(m.compiled) == 0 && len(m.compiledLn) == 0 &&
		len(m.Any) == 0 && len(m.All) == 0 && len(m.Not) == 0 {
		return false
	}
	lower := strings.ToLower(region)
	for _, c := range m.Contains {
		if !strings.Contains(lower, strings.ToLower(c)) {
			return false
		}
	}
	for _, re := range m.compiled {
		if !re.MatchString(region) {
			return false
		}
	}
	for _, re := range m.compiledLn {
		if !lineRegexMatch(re, region) {
			return false
		}
	}
	// `any = [...]` is a disjunction across the array: at least one
	// entry's nested conditions must match. Used by rule files to
	// express "either of these phrases is enough" without splitting
	// the rule into multiple files.
	if len(m.Any) > 0 {
		matched := false
		for i := range m.Any {
			if m.Any[i].match(region) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	// `all = [...]` is conjunctive: every sub-spec must match.
	for i := range m.All {
		if !m.All[i].match(region) {
			return false
		}
	}
	// `not = [...]` is negation: no sub-spec may match.
	for i := range m.Not {
		if m.Not[i].match(region) {
			return false
		}
	}
	return true
}

// lineRegexMatch tests `re` against each line of region, returning
// true if any line matches. Distinct from a bare `Regex` which is
// matched against the entire region as one string — useful for rules
// that want to check "any single line starts with ❯".
func lineRegexMatch(re *regexp.Regexp, region string) bool {
	for _, ln := range strings.Split(region, "\n") {
		if re.MatchString(ln) {
			return true
		}
	}
	return false
}

// compile turns the textual Regex / LineRegex fields into compiled
// regexps. Cached on first call so a rule list scanned every poll
// tick doesn't recompile. A malformed regex is silently dropped —
// the loader is the right place to surface schema errors loudly;
// here we'd rather degrade gracefully than panic the daemon.
func (m *MatchSpec) compile() {
	m.compiled = m.compiled[:0]
	for _, src := range m.Regex {
		if re, err := regexp.Compile(src); err == nil {
			m.compiled = append(m.compiled, re)
		}
	}
	m.compiledLn = m.compiledLn[:0]
	for _, src := range m.LineRegex {
		if re, err := regexp.Compile(src); err == nil {
			m.compiledLn = append(m.compiledLn, re)
		}
	}
	m.hasCompiled = true
}

// parseState maps the TOML state strings to State. Unknown
// values fall to StateUnknown so a typo can't silently produce a
// "blocked" classification.
func parseState(s string) State {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "working", "active":
		return StateActive
	case "blocked", "needs_input":
		return StateNeedsInput
	case "idle":
		return StateIdle
	case "error":
		return StateError
	}
	return StateUnknown
}
