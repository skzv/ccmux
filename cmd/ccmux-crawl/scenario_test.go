package main

import (
	"testing"
)

// TestAllScenariosCompileAndHaveSteps checks that every scenario has at
// least one step. An accidental empty scenario would trivially pass at
// runtime, masking a misconfigured entry.
func TestAllScenariosCompileAndHaveSteps(t *testing.T) {
	for _, s := range allScenarios() {
		if len(s.steps) == 0 {
			t.Errorf("scenario %q has no steps", s.name)
		}
	}
}

// TestScenarioNames_Unique verifies scenario names are unique. The
// --scenario flag selects by name; duplicate names would silently skip
// one of the entries.
func TestScenarioNames_Unique(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range allScenarios() {
		if seen[s.name] {
			t.Errorf("duplicate scenario name %q", s.name)
		}
		seen[s.name] = true
	}
}

// TestRunScenario_AllScreens exercises navigation to all 7 screens
// without panicking or failing an assertion.
func TestRunScenario_AllScreens(t *testing.T) {
	s := findScenario("all-screens")
	if s == nil {
		t.Fatal("all-screens scenario not found")
	}
	if err := runScenario(*s); err != nil {
		t.Errorf("all-screens: %v", err)
	}
}

// TestRunScenario_HelpOverlay opens the help overlay and then closes it,
// asserting the expected text is present/absent respectively.
func TestRunScenario_HelpOverlay(t *testing.T) {
	s := findScenario("help-overlay")
	if s == nil {
		t.Fatal("help-overlay scenario not found")
	}
	if err := runScenario(*s); err != nil {
		t.Errorf("help-overlay: %v", err)
	}
}

// TestRunScenario_NewSessionAbandon opens the new session form and cancels,
// asserting the form title appears then disappears.
func TestRunScenario_NewSessionAbandon(t *testing.T) {
	s := findScenario("new-session-abandon")
	if s == nil {
		t.Fatal("new-session-abandon scenario not found")
	}
	if err := runScenario(*s); err != nil {
		t.Errorf("new-session-abandon: %v", err)
	}
}
