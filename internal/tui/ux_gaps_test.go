package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/scaffold"
)

// TestStartupConfigError_DoesNotReshowTour — with a config.toml that
// doesn't load, the defaults say the tour was never shown, so every
// launch re-opened the first-run tour.
func TestStartupConfigError_DoesNotReshowTour(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := New(config.Defaults(), "test")
	if !a.tour.Active() {
		t.Fatal("setup: a fresh config should open the first-run tour")
	}
	a.SetStartupConfigError(errors.New(`parse config "config.toml": toml: line 3: expected value`))
	if a.tour.Active() {
		t.Error("the first-run tour opened although config.toml failed to load")
	}
}

// TestProjectsC_OpensTheProjectsAgentSection — `c` on a Codex project
// opened Conversations on the Claude section.
func TestProjectsC_OpensTheProjectsAgentSection(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenProjects
	path := "/Users/me/Projects/api"
	a.projectsM.SetProjects([]project.Project{{Name: "api", Path: path, Agent: agent.IDCodex}})
	a.conversationsM.SetList([]conversations.Conversation{
		{ID: "c1", Agent: agent.IDClaude, Project: path},
		{ID: "x1", Agent: agent.IDCodex, Project: path},
	})

	_, cmd := a.Update(keyMsg("c"))
	drill, ok := cmd().(openConversationsForProjectMsg)
	if !ok {
		t.Fatal("`c` did not open the project's conversations")
	}
	m, _ := a.Update(drill)
	a = m.(App)
	if got := a.conversationsM.focusedSectionDef().Agent; got != agent.IDCodex {
		t.Errorf("Conversations opened on the %s section for a Codex project", got)
	}
	if sel := a.conversationsM.Selected(); sel == nil || sel.ID != "x1" {
		t.Errorf("selected %+v, want the Codex conversation", sel)
	}
}

// TestNewProject_ExistingNameIsRefused — "New project" with the name of
// an existing project quietly started a session in it. The form must
// stay open and say the project exists; nothing is started.
func TestNewProject_ExistingNameIsRefused(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	started := 0
	orig := startProjectSession
	startProjectSession = func(context.Context, scaffold.Options) (string, error) {
		started++
		return "c-x", nil
	}
	t.Cleanup(func() { startProjectSession = orig })

	m := newProjects(newAppForTest(t).styles, DefaultKeymap())
	m.SetProjectsRoot(root)
	m, _ = m.Update(keyMsg("n"))
	if m.form == nil {
		t.Fatal("n did not open the new-project form")
	}
	m.form.name.SetValue("taken")
	m, cmd := m.Update(keyMsg("enter"))
	submit, ok := cmd().(newProjectSubmitMsg)
	if !ok {
		t.Fatal("enter did not submit")
	}
	m, cmd = m.Update(submit)
	if m.form == nil {
		t.Fatal("the form closed for a name that already exists")
	}
	if !strings.Contains(m.form.err, "already exists") {
		t.Errorf("form error = %q, want it to say the project exists", m.form.err)
	}
	if cmd != nil {
		cmd()
	}
	if started != 0 {
		t.Errorf("a session was started for the existing project")
	}

	// A new name still creates.
	m.form.name.SetValue("fresh")
	m.form.err = ""
	_, cmd = m.Update(keyMsg("enter"))
	submit = cmd().(newProjectSubmitMsg)
	_, cmd = m.Update(submit)
	if cmd == nil {
		t.Fatal("a new name produced no create command")
	}
	cmd()
	if started != 1 {
		t.Errorf("create for a new name started %d sessions, want 1", started)
	}
}

// TestAgentsClaude_OneSelectionAtATime — the Claude sub-tab drew a "▌"
// on the focused settings row and on the browser's cursor row at once,
// and Enter acted on the settings row while the browser row looked just
// as selected.
func TestAgentsClaude_OneSelectionAtATime(t *testing.T) {
	st := newAppForTest(t).styles
	m := newAgents(st, DefaultKeymap())
	m.active = agent.IDClaude
	m.claude.browser.SetSections("t", []agentBrowserSection{{Title: "Hooks", Items: []agentBrowserItem{
		{Label: "SessionStart", Preview: "x"}, {Label: "Stop", Preview: "y"},
	}}})
	m.SetSize(120, 40)
	count := func() int { return strings.Count(ansi.Strip(m.View(120, 40)), "▌") }
	if !m.claude.focusTop {
		t.Fatal("setup: the settings rows should start with the focus")
	}
	if got := count(); got != 1 {
		t.Errorf("settings focused: %d selection bars on screen, want 1", got)
	}
	// Walk down past the settings rows into the browser.
	for i := 0; i < claudeActionRowCount; i++ {
		m, _ = m.Update(keyMsg("down"))
	}
	if m.claude.focusTop {
		t.Fatal("setup: focus should have moved into the browser")
	}
	if got := count(); got != 1 {
		t.Errorf("browser focused: %d selection bars on screen, want 1", got)
	}
}
