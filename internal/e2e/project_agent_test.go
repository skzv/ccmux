//go:build integration

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestProjectAgentSidecar_NewWritesAttachHonors covers the per-project
// agent sidecar end-to-end through the CLI:
//
//  1. `ccmux new <name> --agent codex` records the choice in
//     <project>/.ccmux/agent and launches the codex stub — not the
//     hardcoded claude default.
//  2. After the session is killed, `ccmux attach <project>` re-creates
//     it from the SIDECAR alone (no --agent flag anywhere): the read
//     path project.ReadAgent → agent.LaunchCmd.
//
// codex is deliberately the non-default agent so "sidecar honored" is
// distinguishable from "claude default fired" (same reasoning as
// agent_test.go). Complements TestProjectNew (bare `ccmux new`) and
// the agent_test.go default/flag cases, which never touch the sidecar.
func TestProjectAgentSidecar_NewWritesAttachHonors(t *testing.T) {
	e := newEnv(t)

	// `ccmux new` execs `tmux attach` last, which fails without a tty
	// — tolerated; the directory, sidecar, and session happen first.
	_, _, _ = e.ccmuxIn(e.Root, "new", "sidecarproj", "--agent", "codex")

	dir := filepath.Join(e.Root, "sidecarproj")
	sidecar := filepath.Join(dir, ".ccmux", "agent")
	if !exists(sidecar) {
		t.Fatalf("`ccmux new --agent codex` did not write the sidecar %s", sidecar)
	}
	if got := strings.TrimSpace(readFile(t, sidecar)); got != "codex" {
		t.Fatalf("sidecar content = %q, want codex", got)
	}

	const session = "c-sidecarproj"
	if !e.hasSession(session) {
		t.Fatal("`ccmux new` did not start session c-sidecarproj")
	}
	waitForStubAgent(t, e, session, "codex")

	// Kill, then attach with no agent flag anywhere: the session must
	// come back running codex purely from the sidecar.
	if _, stderr, err := e.ccmux("kill", "sidecarproj"); err != nil {
		t.Fatalf("ccmux kill sidecarproj: %v\nstderr: %s", err, stderr)
	}
	if e.hasSession(session) {
		t.Fatal("session c-sidecarproj still present after kill")
	}
	_, _, _ = e.ccmux("attach", dir)
	if !e.hasSession(session) {
		t.Fatal("`ccmux attach` did not re-create session c-sidecarproj")
	}
	waitForStubAgent(t, e, session, "codex")
}

// waitForStubAgent polls a session's pane until the stub-agent marker
// appears, then asserts it names `want` — and not claude, when want is
// a different agent (the failure mode every sidecar bug collapses to).
func waitForStubAgent(t *testing.T, e *Env, session, want string) {
	t.Helper()
	var pane string
	if !waitFor(5*time.Second, func() bool {
		pane = e.capturePane(session)
		return strings.Contains(pane, "ccmux-stub-agent=")
	}) {
		t.Fatalf("agent stub never wrote its marker in %s\npane:\n%s", session, pane)
	}
	if !strings.Contains(pane, "ccmux-stub-agent="+want) {
		t.Errorf("session %s launched the wrong agent — want %s\npane:\n%s", session, want, pane)
	}
	if want != "claude" && strings.Contains(pane, "ccmux-stub-agent=claude") {
		t.Errorf("session %s launched claude despite the %s sidecar\npane:\n%s", session, want, pane)
	}
}
