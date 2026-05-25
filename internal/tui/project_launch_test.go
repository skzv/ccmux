package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/project"
)

// These tests pin the TUI's project-launch resolution against the
// "everything launches claude" bug. Two helpers — launchCmdForProject
// (Project in hand) and launchCmdForProjectPath (path on disk) — both
// feed every project-attach call site, and both must agree on what
// command the project's sidecar maps to. A future change that
// hardcodes "claude" in either spot is caught by the source-grep
// audit in internal/agent/no_hardcode_audit_test.go; these tests
// pin the positive behavior so a refactor that moves the resolution
// elsewhere doesn't silently regress.

// TestLaunchCmdForProject_PerAgent — every supported agent ID on a
// project.Project resolves to the agent's own LaunchCmd(true). The
// `true` is load-bearing: project attaches resume the existing
// conversation, and a regression that passed `false` would silently
// start fresh chats every time a user hit Enter on Projects.
func TestLaunchCmdForProject_PerAgent(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			p := project.Project{Agent: a.ID()}
			got := launchCmdForProject(p)
			want := a.LaunchCmd(true)
			if got != want {
				t.Errorf("launchCmdForProject(Agent=%q) = %q, want %q",
					a.ID(), got, want)
			}
			if !strings.HasPrefix(got, a.Binary()) {
				t.Errorf("launchCmdForProject(Agent=%q) = %q, expected to start with binary %q",
					a.ID(), got, a.Binary())
			}
			switch a.ID() {
			case agent.IDCursor:
				if !strings.Contains(got, " resume") {
					t.Errorf("launchCmdForProject(Agent=%q) = %q, expected resume subcommand (project attach resumes)",
						a.ID(), got)
				}
			default:
				if !strings.Contains(got, "--continue") {
					t.Errorf("launchCmdForProject(Agent=%q) = %q, expected --continue (project attach resumes)",
						a.ID(), got)
				}
			}
		})
	}
}

// TestLaunchCmdForProject_EmptyAgentDefaultsToClaude — projects
// scaffolded before the sidecar landed have Agent == "". By
// agent.ByID's contract that resolves to Claude. This is the
// back-compat invariant: a pre-multi-agent project must keep
// launching claude when the user hits Enter.
func TestLaunchCmdForProject_EmptyAgentDefaultsToClaude(t *testing.T) {
	got := launchCmdForProject(project.Project{Agent: ""})
	want := agent.Claude{}.LaunchCmd(true)
	if got != want {
		t.Errorf("empty agent = %q, want %q (claude back-compat)", got, want)
	}
}

// TestLaunchCmdForProjectPath_HonorsSidecar — the path-flavoured
// helper reads `.ccmux/agent` and uses that. Without this, the project
// menu's "Start a new session" path would hardcode claude regardless
// of the sidecar.
func TestLaunchCmdForProjectPath_HonorsSidecar(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, ".ccmux"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := project.SetAgent(dir, a.ID()); err != nil {
				t.Fatal(err)
			}
			got := launchCmdForProjectPath(dir)
			want := a.LaunchCmd(true)
			if got != want {
				t.Errorf("launchCmdForProjectPath(sidecar=%q) = %q, want %q",
					a.ID(), got, want)
			}
		})
	}
}

// TestLaunchCmdForProjectPath_MissingSidecarFallsBackToClaude — the
// path helper must default to claude when the sidecar is missing.
// project.ReadAgent already does the IDClaude fallback; this test
// confirms the launch helper follows.
func TestLaunchCmdForProjectPath_MissingSidecarFallsBackToClaude(t *testing.T) {
	dir := t.TempDir()
	got := launchCmdForProjectPath(dir)
	want := agent.Claude{}.LaunchCmd(true)
	if got != want {
		t.Errorf("missing sidecar = %q, want %q", got, want)
	}
}

// TestLaunchCmdForProject_AgreesWithPathFlavor — both helpers must
// produce the same string for the same project. A divergence would
// mean Enter-on-Projects launches one agent but the "Start new"
// branch of the picker launches another for the same project.
func TestLaunchCmdForProject_AgreesWithPathFlavor(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, ".ccmux"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := project.SetAgent(dir, a.ID()); err != nil {
				t.Fatal(err)
			}
			p := project.Project{Path: dir, Agent: a.ID()}
			if launchCmdForProject(p) != launchCmdForProjectPath(dir) {
				t.Errorf("project (%q) and path (%q) flavors disagree for agent=%q",
					launchCmdForProject(p), launchCmdForProjectPath(dir), a.ID())
			}
		})
	}
}
