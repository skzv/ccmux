package main

import (
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

// TestTelegramSignals is the white-box check of the poll-loop hook
// condition: when does a transition queue an alert vs a resolve?
func TestTelegramSignals(t *testing.T) {
	cases := []struct {
		name                   string
		prev, next             agent.State
		attached               bool
		wantAlert, wantResolve bool
	}{
		{"enters needs_input unattended → alert", agent.StateActive, agent.StateNeedsInput, false, true, false},
		{"enters needs_input attended → no alert", agent.StateActive, agent.StateNeedsInput, true, false, false},
		{"leaves needs_input → resolve", agent.StateNeedsInput, agent.StateActive, false, false, true},
		{"needs_input attended (no change) → resolve", agent.StateNeedsInput, agent.StateNeedsInput, true, false, true},
		{"stays needs_input unattended → nothing", agent.StateNeedsInput, agent.StateNeedsInput, false, false, false},
		{"unrelated transition → nothing", agent.StateActive, agent.StateIdle, false, false, false},
		{"needs_input → idle → resolve", agent.StateNeedsInput, agent.StateIdle, false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			alert, resolve := telegramSignals(c.prev, c.next, c.attached)
			if alert != c.wantAlert || resolve != c.wantResolve {
				t.Errorf("telegramSignals(%s→%s attached=%v) = (alert=%v resolve=%v), want (%v %v)",
					c.prev, c.next, c.attached, alert, resolve, c.wantAlert, c.wantResolve)
			}
		})
	}
}
