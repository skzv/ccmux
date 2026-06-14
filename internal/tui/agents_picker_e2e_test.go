package tui

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// These tests drive the REAL App for the Agents screen — keypress
// through App.Update, render through App.agentsM.View — because the
// model-picker bugs lived exactly in that wiring: the key reached the
// sub-model fine, but the screen rendered ViewBody (which omits the
// picker) and a global `M` handler swallowed the ccmux-model picker key
// before it ever reached the screen. Sub-model-only tests passed while
// the feature was dead; these would have failed.

// buildAgentsApp returns an App parked on the Agents screen with Claude
// active and an isolated ~/.claude so reload() never touches the real
// home. width/height take the wide layout.
func buildAgentsApp(t *testing.T) App {
	t.Helper()
	// Isolate Claude config dir so claudeModel.reload() reads a sandbox.
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	st := styles.Default()
	km := DefaultKeymap()
	cfg := config.Defaults()
	a := App{
		cfg:     cfg,
		styles:  st,
		keys:    km,
		version: "v0.0.0-test",
		screen:  ScreenAgents,
	}
	a.width, a.height = 160, 44
	a.agentsM = newAgents(st, km)
	a.agentsM.active = agent.IDClaude
	a.dashboard = newDashboard(st, km)
	return a
}

// agentsBody renders the Agents screen the way the app does.
func agentsBody(a App) string { return a.agentsM.View(a.width, a.height) }

// TestE2E_AgentsModelPickerOpensAndRenders is THE regression test for
// the dead `m` key. Press `m` on the real App; the Agents screen must
// then render the model picker. Before the fix the picker state was set
// but ViewBody never rendered it, so this output stayed on the config
// listing.
func TestE2E_AgentsModelPickerOpensAndRenders(t *testing.T) {
	a := buildAgentsApp(t)

	// Baseline: no picker visible.
	if strings.Contains(agentsBody(a), "Pick default model") {
		t.Fatal("picker visible before pressing `m`")
	}

	a, _ = updateApp(t, a, keyMsg("m"))
	if !a.agentsM.claude.PickerOpen() {
		t.Fatal("`m` did not open the picker through App.Update — key never reached the sub-model")
	}
	out := agentsBody(a)
	if !strings.Contains(out, "Pick default model") {
		t.Errorf("Agents screen did not render the model picker after `m`.\n--- got ---\n%s", out)
	}
	// The picker lists the known model aliases.
	if !strings.Contains(strings.ToLower(out), "opus") {
		t.Error("model picker did not list the model options")
	}
}

// TestE2E_AgentsModelPickerNavigatesAndCloses — arrow keys move within
// the open picker and esc dismisses it, all through the real App.
func TestE2E_AgentsModelPickerNavigatesAndCloses(t *testing.T) {
	a := buildAgentsApp(t)
	a, _ = updateApp(t, a, keyMsg("m"))

	// The picker pre-positions its cursor on the current model (which
	// could be anywhere, incl. a list boundary). Drive to the top first
	// so the assertions don't depend on where it opened.
	for i := 0; i < 16; i++ {
		a, _ = updateApp(t, a, keyMsg("up"))
	}
	if a.agentsM.claude.pickerCursor != 0 {
		t.Fatalf("repeated up did not reach the top of the list; cursor=%d", a.agentsM.claude.pickerCursor)
	}
	a, _ = updateApp(t, a, keyMsg("down"))
	if a.agentsM.claude.pickerCursor != 1 {
		t.Errorf("down from the top did not advance the cursor; cursor=%d", a.agentsM.claude.pickerCursor)
	}

	a, _ = updateApp(t, a, keyMsg("esc"))
	if a.agentsM.claude.PickerOpen() {
		t.Error("esc did not close the picker")
	}
	if strings.Contains(agentsBody(a), "Pick default model") {
		t.Error("picker still rendered after esc")
	}
}

// TestE2E_AgentsEffortPickerOpens — the `e` effort picker is the same
// render path; pin it too so a future ViewBody/​View refactor can't
// re-break one without the other.
func TestE2E_AgentsEffortPickerOpens(t *testing.T) {
	a := buildAgentsApp(t)
	a, _ = updateApp(t, a, keyMsg("e"))
	if !a.agentsM.claude.PickerOpen() {
		t.Fatal("`e` did not open the effort picker")
	}
	if !strings.Contains(agentsBody(a), "Pick reasoning effort") {
		t.Error("effort picker did not render")
	}
}

// TestE2E_AgentsCcmuxModelPickerNotShadowedByMatrix — capital `M` on the
// Agents screen must open the ccmux-model picker, NOT the Matrix easter
// egg. The global `M` handler used to fire first and swallow the key.
func TestE2E_AgentsCcmuxModelPickerNotShadowedByMatrix(t *testing.T) {
	a := buildAgentsApp(t)
	a, _ = updateApp(t, a, keyMsg("M"))
	if a.matrix.Active() {
		t.Fatal("capital M opened the Matrix easter egg on the Agents screen — it shadowed the ccmux-model picker")
	}
	if !a.agentsM.claude.PickerOpen() {
		t.Fatal("capital M did not open the ccmux-model picker on the Agents screen")
	}
	if !strings.Contains(agentsBody(a), "Pin a model") {
		t.Error("ccmux-model picker did not render")
	}
}

// TestE2E_MatrixStillOpensOffAgentsScreen — the gating that lets the
// Agents screen own `M` must NOT break the Matrix easter egg on every
// other screen.
func TestE2E_MatrixStillOpensOffAgentsScreen(t *testing.T) {
	a := buildAgentsApp(t)
	a.screen = ScreenSessions
	// Sessions screen needs a sessions model to route through; the
	// global M handler fires before screen routing, so this is enough.
	a.sessionsM = newSessions(a.styles, a.keys)
	a, _ = updateApp(t, a, keyMsg("M"))
	if !a.matrix.Active() {
		t.Error("Matrix easter egg no longer opens on non-Agents screens — the gating over-reached")
	}
}
