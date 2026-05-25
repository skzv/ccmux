package tui

import (
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestIndexOfDefaultProjectAgent_MatchesEachAgent — the new-project
// form's default-agent helper must resolve every canonical agent ID
// to its position in the agents slice. If this breaks, setting a
// default in config would silently fall back to claude (idx 0).
func TestIndexOfDefaultProjectAgent_MatchesEachAgent(t *testing.T) {
	agents := []agent.Agent{agent.Claude{}, agent.Codex{}, agent.Antigravity{}, agent.Cursor{}}
	cases := []struct {
		def  string
		want int
	}{
		{"claude", 0},
		{"codex", 1},
		{"antigravity", 2},
		{"cursor", 3},
		// Back-compat alias from the rebrand — same canonical ID lookup
		// path should resolve it.
		{"gemini", 2},
		// Whitespace + casing tolerance — Settings input shouldn't
		// fail just because the user typed "  Codex  ".
		{"  Codex  ", 1},
		{"ANTIGRAVITY", 2},
	}
	for _, tc := range cases {
		t.Run(tc.def, func(t *testing.T) {
			if got := indexOfDefaultProjectAgent(agents, tc.def); got != tc.want {
				t.Errorf("indexOfDefaultProjectAgent(%q) = %d, want %d", tc.def, got, tc.want)
			}
		})
	}
}

// TestIndexOfDefaultProjectAgent_UnknownFallsBackToFirst — invalid /
// unknown / empty / "shell" all resolve to row 0 (first installed
// agent). The "shell" case matters specifically because the new-
// project form doesn't carry a shell option — falling back to claude
// is the right behavior there.
func TestIndexOfDefaultProjectAgent_UnknownFallsBackToFirst(t *testing.T) {
	agents := []agent.Agent{agent.Claude{}, agent.Codex{}, agent.Antigravity{}, agent.Cursor{}}
	for _, def := range []string{"", "shell", "SHELL", "imaginary-llm", "gpt-4"} {
		t.Run(def, func(t *testing.T) {
			if got := indexOfDefaultProjectAgent(agents, def); got != 0 {
				t.Errorf("def=%q got idx %d, want 0 (fallback)", def, got)
			}
		})
	}
}

// TestIndexOfDefaultProjectAgent_MissingAgent — when the user has
// set their default to codex but codex isn't installed, the form
// drops back to row 0 rather than crashing or showing a phantom row.
func TestIndexOfDefaultProjectAgent_MissingAgent(t *testing.T) {
	// Only claude is installed.
	agents := []agent.Agent{agent.Claude{}}
	if got := indexOfDefaultProjectAgent(agents, "codex"); got != 0 {
		t.Errorf("missing codex → fallback to 0, got %d", got)
	}
	if got := indexOfDefaultProjectAgent(agents, "antigravity"); got != 0 {
		t.Errorf("missing antigravity → fallback to 0, got %d", got)
	}
}

// TestNewProjectForm_HonorsDefaultAgent — end-to-end: passing a
// default agent to the form constructor pre-selects its row. This is
// the critical wiring — without it, setting agents.default in config
// would do nothing visible in the new-project form.
func TestNewProjectForm_HonorsDefaultAgent(t *testing.T) {
	st := styles.Default()
	cases := []struct {
		def  string
		want agent.ID
	}{
		{"codex", agent.IDCodex},
		{"antigravity", agent.IDAntigravity},
		{"cursor", agent.IDCursor},
		{"claude", agent.IDClaude},
	}
	for _, tc := range cases {
		t.Run(tc.def, func(t *testing.T) {
			f := newNewProjectForm(st, nil, tc.def)
			// agents may be filtered by AllInstalled on the test
			// machine. We pin behavior using the form's internal
			// agents slice: the picked index must select an entry
			// whose ID matches `want` — unless that agent isn't
			// installed at all, in which case the test asserts the
			// safe fallback (idx 0).
			pickedID := f.agents[f.agentIdx].ID()
			present := false
			for _, a := range f.agents {
				if a.ID() == tc.want {
					present = true
				}
			}
			if present {
				if pickedID != tc.want {
					t.Errorf("default %q picked %q, want %q (it IS installed)", tc.def, pickedID, tc.want)
				}
			} else {
				if f.agentIdx != 0 {
					t.Errorf("default %q not installed; should fall back to idx 0, got %d", tc.def, f.agentIdx)
				}
			}
		})
	}
}

// TestProjectsModel_SetDefaultAgent_Propagates — the App-side hook
// pushes the cfg value into the model so the next "n" press opens
// the form with the right pre-selection. Without this, the field
// would be wired-but-not-fed.
func TestProjectsModel_SetDefaultAgent_Propagates(t *testing.T) {
	m := newProjects(styles.Default(), DefaultKeymap())
	m.SetDefaultAgent("codex")
	if m.defaultAgent != "codex" {
		t.Errorf("defaultAgent = %q, want codex", m.defaultAgent)
	}
}
