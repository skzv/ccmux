package agent

import (
	"github.com/skzv/ccmux/internal/agentdetect"
	"github.com/skzv/ccmux/internal/claude"
)

// TurnView is what one capture of an agent's pane shows about its turn,
// beyond the state it classifies as: the evidence the daemon weighs to
// tell the agent working from the user typing into it (a body change
// alone is either). See ReadTurn.
type TurnView struct {
	// Busy reports that the body shows a turn running right now, in a
	// form typing can't produce: a working rule of the agent's own rule
	// file matched the body (a working footer such as OpenCode's `esc
	// interrupt`), or the agent's TurnReader says so (Claude's `…
	// (12s · esc to interrupt)` status line).
	Busy bool
	// Separated reports that the agent keeps its own output apart from
	// what the user types (it implements TurnReader). Only then are
	// Output and HasInput meaningful.
	Separated bool
	// Output is the tail of what the agent printed above its input area
	// — it changes when the agent works and not when the user types —
	// and HasInput whether that input area is on screen at all.
	Output   string
	HasInput bool
}

// TurnReader is the optional interface an Agent implements when its
// pane keeps the user's input apart from its own output — an input box
// pinned to the bottom of the pane — so the daemon can read a change
// above the box as the agent working and a change inside it as typing.
// Separated is set by ReadTurn, not by the implementation.
type TurnReader interface {
	Agent
	ReadTurn(pane string) TurnView
}

// ReadTurn reads pane for a's TurnView: the agent's own TurnReader
// when it has one, plus its rule file's body working rules.
func ReadTurn(a Agent, pane string) TurnView {
	var v TurnView
	if tr, ok := a.(TurnReader); ok {
		v = tr.ReadTurn(pane)
		v.Separated = true
	}
	v.Busy = v.Busy || bodyWorking(a.ID(), pane)
	return v
}

// bodyWorking reports whether the agent's rules, shown the body alone
// (no title), read it as working: its highest-priority matching rule
// is a body rule with state "working".
func bodyWorking(id ID, pane string) bool {
	if !hasBodyWorkingRule(id) {
		return false
	}
	res := evaluateRules(id, pane, "")
	return res.MatchedRuleID != "" && !res.SkipStateUpdate && res.State == agentdetect.StateActive
}

// hasBodyWorkingRule reports whether any of the agent's rules reads the
// body (not the title) as working, so agents without one don't pay for
// a second rule evaluation per tick.
func hasBodyWorkingRule(id ID) bool {
	rules := rulesFor(id)
	for i := range rules {
		if rules[i].Region != "osc_title" && rules[i].Region != "osc_progress" && rules[i].Verdict() == agentdetect.StateActive {
			return true
		}
	}
	return false
}

// ReadTurn implements TurnReader: Claude Code's input box sits at the
// bottom of its pane, under everything it prints (internal/claude).
func (Claude) ReadTurn(pane string) TurnView {
	t := claude.ReadTurn(pane)
	return TurnView{Busy: t.Busy, Output: t.Output, HasInput: t.HasInput}
}
