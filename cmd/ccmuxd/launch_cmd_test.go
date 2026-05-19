package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/project"
)

// These tests pin the daemon's launch-command resolution against the
// "everything launches claude" regression. The bug was that the
// /v1/sessions endpoint always ran `claude --continue || claude ||
// zsh` regardless of the project's .ccmux/agent sidecar, and
// /v1/sessions/bare always ran $SHELL regardless of the picker's
// agent selection. Both are gone — and these tests fail if anyone
// re-introduces them.

// TestBareSessionLaunchCmd_RequestWins — an explicit agent in the
// request body overrides the daemon's configured default. The picker
// selection / `ccmux shell --agent` choice must beat the global
// default, otherwise per-session overrides would be invisible.
func TestBareSessionLaunchCmd_RequestWins(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			got := bareSessionLaunchCmd(string(a.ID()), "claude")
			want := a.LaunchCmd(false)
			if got != want {
				t.Errorf("bareSessionLaunchCmd(req=%q, def=claude) = %q, want %q",
					a.ID(), got, want)
			}
		})
	}
}

// TestBareSessionLaunchCmd_ConfigDefault — when the request omits
// Agent, the daemon's sessions.default_agent is honored. Each agent
// id must resolve to its own LaunchCmd, not a hardcoded claude.
func TestBareSessionLaunchCmd_ConfigDefault(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			got := bareSessionLaunchCmd("", string(a.ID()))
			want := a.LaunchCmd(false)
			if got != want {
				t.Errorf("bareSessionLaunchCmd(req=\"\", def=%q) = %q, want %q",
					a.ID(), got, want)
			}
		})
	}
}

// TestBareSessionLaunchCmd_ShellExplicit — the literal "shell" in
// either slot short-circuits to $SHELL. A user who picked "shell" in
// the form must not get an agent silently injected by the config
// default.
func TestBareSessionLaunchCmd_ShellExplicit(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/zsh")
	// Request says shell, even though config default is an agent.
	if got := bareSessionLaunchCmd("shell", "claude"); got != "/usr/bin/zsh" {
		t.Errorf("explicit shell req with claude default = %q, want /usr/bin/zsh", got)
	}
	// Config default is shell, request empty.
	if got := bareSessionLaunchCmd("", "shell"); got != "/usr/bin/zsh" {
		t.Errorf("empty req, shell default = %q, want /usr/bin/zsh", got)
	}
}

// TestBareSessionLaunchCmd_GeminiAlias — the back-compat alias must
// still resolve to Antigravity. Removing the alias would silently
// break any project's saved settings.toml that still says "gemini".
func TestBareSessionLaunchCmd_GeminiAlias(t *testing.T) {
	got := bareSessionLaunchCmd("gemini", "")
	want := agent.Antigravity{}.LaunchCmd(false)
	if got != want {
		t.Errorf("bareSessionLaunchCmd(\"gemini\", \"\") = %q, want %q (antigravity)", got, want)
	}
}

// TestBareSessionLaunchCmd_UnknownFallsToShell — a typo in the
// config or wire input must not panic or default to claude. Falls to
// $SHELL so the user sees a working pane and a recognizable prompt.
func TestBareSessionLaunchCmd_UnknownFallsToShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	if got := bareSessionLaunchCmd("gpt-7", "also-bogus"); got != "/bin/zsh" {
		t.Errorf("unknown agent fallthrough = %q, want /bin/zsh", got)
	}
}

// TestProjectLaunchCmd_HonorsSidecar — projectLaunchCmd reads the
// project's .ccmux/agent sidecar and uses that agent's LaunchCmd.
// Without this, /v1/sessions would launch claude in every project
// regardless of what `ccmux a` set.
func TestProjectLaunchCmd_HonorsSidecar(t *testing.T) {
	for _, a := range agent.All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, ".ccmux"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := project.SetAgent(dir, a.ID()); err != nil {
				t.Fatal(err)
			}
			got := projectLaunchCmd(dir, true)
			want := a.LaunchCmd(true)
			if got != want {
				t.Errorf("projectLaunchCmd(%q sidecar=%q) = %q, want %q",
					dir, a.ID(), got, want)
			}
			// Sanity: the command must actually invoke this agent's
			// binary at the start. Catches "regression to claude"
			// even if LaunchCmd itself were tampered with.
			if !strings.HasPrefix(got, a.Binary()) {
				t.Errorf("projectLaunchCmd(%q sidecar=%q) = %q, expected to start with %q",
					dir, a.ID(), got, a.Binary())
			}
		})
	}
}

// TestProjectLaunchCmd_MissingSidecarFallsBackToClaude — a project
// without a sidecar (pre-multi-agent scaffold) must keep working.
// project.ReadAgent's documented contract returns IDClaude on a
// missing file; this test pins that the daemon's launch path
// follows.
func TestProjectLaunchCmd_MissingSidecarFallsBackToClaude(t *testing.T) {
	dir := t.TempDir() // no .ccmux written
	got := projectLaunchCmd(dir, true)
	want := agent.Claude{}.LaunchCmd(true)
	if got != want {
		t.Errorf("projectLaunchCmd(no sidecar) = %q, want %q", got, want)
	}
}
