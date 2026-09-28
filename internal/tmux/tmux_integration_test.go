//go:build integration

package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolatedServer points every tmux call in this test at a private tmux
// server (TMUX_TMPDIR sandbox, $TMUX cleared) and kills it afterwards.
// It never touches the user's own tmux server.
func isolatedServer(t *testing.T) context.Context {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir, err := os.MkdirTemp("/tmp", "ccmt") // short: sockaddr_un limit
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestIntegration_DottedSessionNames — tmux keeps "." in session names,
// and a bare `-t =api.v2` is parsed as session "api", pane "v2". Every
// exact-target helper must still find, rename and kill such a session.
func TestIntegration_DottedSessionNames(t *testing.T) {
	ctx := isolatedServer(t)
	if err := New(ctx, "api.v2", os.TempDir(), "sleep 300"); err != nil {
		t.Fatal(err)
	}
	// Older tmux (e.g. 3.4, Ubuntu 24.04) stores "api.v2" as "api_v2",
	// so a dotted session can't exist there — which is also why the
	// daemon refuses dotted names. Only a tmux that keeps the dot can
	// exercise the targets; anywhere else, skip rather than pass
	// vacuously. Decided from list-sessions, not Has, so a Has broken
	// by target parsing still fails below.
	sessions, err := List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored := ""
	for _, s := range sessions {
		if s.Name == "api.v2" || s.Name == "api_v2" {
			stored = s.Name
		}
	}
	if stored == "api_v2" {
		t.Skip("this tmux rewrites '.' to '_' in session names; dotted targets can't occur")
	}
	if stored != "api.v2" {
		t.Fatalf("session not listed after New (sessions: %+v)", sessions)
	}
	if ok, err := Has(ctx, "api.v2"); err != nil || !ok {
		t.Fatalf("Has(api.v2) = %v, %v; want true", ok, err)
	}
	// AttachArgs' target must resolve to the dotted session.
	args := AttachArgs("api.v2", false)
	target := args[len(args)-1]
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", target, "#{session_name}").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "api.v2" {
		t.Fatalf("attach target %q resolves to %q (err %v), want api.v2", target, out, err)
	}
	if err := Rename(ctx, "api.v2", "api.v3"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := Kill(ctx, "api.v3"); err != nil {
		t.Fatalf("Kill(api.v3): %v", err)
	}
	if ok, _ := Has(ctx, "api.v3"); ok {
		t.Error("session still exists after Kill")
	}
}

// TestIntegration_ExactTargetDoesNotPrefixMatch — killing a session
// that no longer exists must not fall through to a longer name.
func TestIntegration_ExactTargetDoesNotPrefixMatch(t *testing.T) {
	ctx := isolatedServer(t)
	if err := New(ctx, "c-foo-app", os.TempDir(), "sleep 300"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := Has(ctx, "c-foo"); ok {
		t.Error("Has(c-foo) matched c-foo-app")
	}
	_ = Kill(ctx, "c-foo")
	if ok, _ := Has(ctx, "c-foo-app"); !ok {
		t.Error("Kill(c-foo) killed c-foo-app")
	}
}

// TestIntegration_SendTextStartingWithDash — text that looks like a
// tmux flag must be typed, not parsed: "- fix the bug" (a markdown
// bullet sent through MCP send_keys) used to fail with "invalid flag".
func TestIntegration_SendTextStartingWithDash(t *testing.T) {
	ctx := isolatedServer(t)
	if err := New(ctx, "c-dash", os.TempDir(), "cat"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"- fix the bug", "-1", "--help", "-R"} {
		if err := SendText(ctx, "c-dash", text); err != nil {
			t.Errorf("SendText(%q): %v", text, err)
		}
		if err := SendKeys(ctx, "c-dash", "Enter"); err != nil {
			t.Fatalf("SendKeys(Enter): %v", err)
		}
	}
	if err := SendKeys(ctx, "c-dash", "-x"); err != nil {
		t.Errorf("SendKeys(%q): %v", "-x", err)
	}
	var pane string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pane, _ = CapturePane(ctx, "c-dash", 20)
		if strings.Contains(pane, "-R") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, want := range []string{"- fix the bug", "-1", "--help", "-R"} {
		if !strings.Contains(pane, want) {
			t.Errorf("pane missing %q:\n%s", want, pane)
		}
	}
}

// TestIntegration_NewWithAgentTagsSession — the chained
// `new-session … ; set-option …` form must really tag the session it
// creates (and only it) on a real tmux.
func TestIntegration_NewWithAgentTagsSession(t *testing.T) {
	ctx := isolatedServer(t)
	if err := New(ctx, "c-tag-sibling", os.TempDir(), "sleep 300"); err != nil {
		t.Fatal(err)
	}
	if err := NewWithAgent(ctx, "c-tag", os.TempDir(), "sleep 300", ShellAgentTag); err != nil {
		t.Fatalf("NewWithAgent: %v", err)
	}
	sessions, err := List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range sessions {
		got[s.Name] = s.Agent
	}
	if got["c-tag"] != ShellAgentTag {
		t.Errorf("c-tag agent = %q, want %q (sessions %+v)", got["c-tag"], ShellAgentTag, sessions)
	}
	if got["c-tag-sibling"] != "" {
		t.Errorf("tag leaked onto another session: %q", got["c-tag-sibling"])
	}
}

// TestIntegration_ListNoServerVersusUnreachable — tmux exits 1 both
// when there is no server and when it can't reach one. Only the first
// is "no sessions": reading an unreachable server as empty made the
// daemon forget every session and announce each one as killed.
func TestIntegration_ListNoServerVersusUnreachable(t *testing.T) {
	ctx := isolatedServer(t)

	// No socket yet: "error connecting to … (No such file or directory)".
	if tss, err := List(ctx); err != nil || len(tss) != 0 {
		t.Fatalf("no server: List = %v, %v; want empty, no error", tss, err)
	}

	if err := New(ctx, "c-list", os.TempDir(), "sleep 300"); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 { // root ignores the socket's permissions
		sock := filepath.Join(os.Getenv("TMUX_TMPDIR"), fmt.Sprintf("tmux-%d", os.Getuid()), "default")
		if err := os.Chmod(sock, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(sock, 0o700) }) // before isolatedServer's kill-server
		tss, err := List(ctx)
		if err == nil {
			t.Errorf("unreachable server (socket permission denied): List = %v with no error, want an error", tss)
		}
		if err := os.Chmod(sock, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	// A server that exited leaves its socket file: "no server running".
	if err := exec.CommandContext(ctx, "tmux", "kill-server").Run(); err != nil {
		t.Fatal(err)
	}
	if tss, err := List(ctx); err != nil || len(tss) != 0 {
		t.Errorf("after kill-server: List = %v, %v; want empty, no error", tss, err)
	}
}

// TestIntegration_StartDirWithHashIsKeptVerbatim — on a real tmux, a
// session started in a directory containing `#` must record and run in
// exactly that directory, not a format-expanded one.
func TestIntegration_StartDirWithHashIsKeptVerbatim(t *testing.T) {
	ctx := isolatedServer(t)
	dir := filepath.Join(t.TempDir(), "with#hash", "x#{session_id}#h")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := New(ctx, "c-hash", dir, "sleep 300"); err != nil {
		t.Fatal(err)
	}
	sessions, err := List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var path string
	for _, s := range sessions {
		if s.Name == "c-hash" {
			path = s.Path
		}
	}
	if path != dir {
		t.Errorf("session_path = %q, want %q", path, dir)
	}
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", exactPane("c-hash"), "#{pane_current_path}").Output()
	if err != nil {
		t.Fatal(err)
	}
	// pane_current_path is the resolved cwd (/private/var/… on macOS).
	want, _ := filepath.EvalSymlinks(dir)
	if got := strings.TrimSpace(string(out)); got != want {
		t.Errorf("pane runs in %q, want %q", got, want)
	}
}

// TestIntegration_ListPanesFindsTheOriginalPane — with a second window
// active and a split made in front of the original pane, the session's
// bare target reads the wrong pane; ListPanes + OldestPane must still
// find the pane the session was created with, report its OSC title and
// size, and CapturePaneID must read it.
func TestIntegration_ListPanesFindsTheOriginalPane(t *testing.T) {
	ctx := isolatedServer(t)
	agentCmd := `printf '\033]2;agent title\007'; echo AGENT-PANE; exec sleep 300`
	if err := New(ctx, "c-panes", os.TempDir(), agentCmd); err != nil {
		t.Fatal(err)
	}
	tmuxRun := func(args ...string) {
		t.Helper()
		if out, err := command(ctx, "tmux", args...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v (%s)", args, err, out)
		}
	}
	tmuxRun("split-window", "-d", "-b", "-t", "=c-panes:", "sh -c 'echo SPLIT-PANE; exec sleep 300'")
	tmuxRun("new-window", "-t", "=c-panes:", "sh -c 'echo SHELL-WINDOW; exec sleep 300'")

	var panes []Pane
	deadline := time.Now().Add(5 * time.Second)
	for {
		var err error
		if panes, err = ListPanes(ctx, "c-panes"); err != nil {
			t.Fatal(err)
		}
		if p, ok := OldestPane(panes); ok && p.Title == "agent title" || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(panes) != 3 {
		t.Fatalf("ListPanes = %+v, want 3 panes", panes)
	}
	agentP, ok := OldestPane(panes)
	if !ok || agentP.Title != "agent title" || agentP.Width <= 0 || agentP.Height <= 0 {
		t.Fatalf("OldestPane = %+v (panes %+v), want the agent pane with its title and size", agentP, panes)
	}
	if agentP.Index == 0 || agentP.Active {
		t.Errorf("setup: agent pane %+v should have been pushed off index 0 and deactivated", agentP)
	}
	body, err := CapturePaneID(ctx, agentP.ID, 10)
	if err != nil || !strings.Contains(body, "AGENT-PANE") {
		t.Errorf("CapturePaneID(%s) = %q, %v; want the agent pane's output", agentP.ID, body, err)
	}
	active, err := CapturePane(ctx, "c-panes", 10)
	if err != nil || strings.Contains(active, "AGENT-PANE") {
		t.Errorf("setup: the session target should read the active shell window, got %q (%v)", active, err)
	}

	if _, err := ListPanes(ctx, "c-pan"); err == nil {
		t.Error("ListPanes matched a session by prefix")
	}
}

// TestIntegration_TargetIDPrefixes pins, on a real tmux, what
// ValidTarget's doc says about ID-looking names in the exact `=name:`
// target: a leading "$" is a session ID (so it must be refused), a
// leading "%" or "@" is only a pane/window ID in a bare target (so it is
// allowed, and finds the session so named).
func TestIntegration_TargetIDPrefixes(t *testing.T) {
	ctx := isolatedServer(t)
	for _, name := range []string{"c-first", "c-victim"} {
		if err := New(ctx, name, os.TempDir(), "sleep 300"); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(session string) (sid, wid, pid string) {
		t.Helper()
		out, err := command(ctx, "tmux", "display-message", "-p", "-t", exactSession(session), "#{session_id} #{window_id} #{pane_id}").Output()
		f := strings.Fields(string(out))
		if err != nil || len(f) != 3 {
			t.Fatalf("ids of %s: %q, %v", session, out, err)
		}
		return f[0], f[1], f[2]
	}
	sid, wid, pid := ids("c-victim") // e.g. $1 @1 %1
	sessionAt := func(target string) string {
		out, _ := command(ctx, "tmux", "display-message", "-p", "-t", target, "#{session_name}").Output()
		return strings.TrimSpace(string(out))
	}

	// "$<n>": the exact target is still the session with that ID.
	if got := sessionAt(exactSession(sid)); got != "c-victim" {
		t.Skipf("this tmux resolves %s to %q, not c-victim by ID; nothing to pin", exactSession(sid), got)
	}
	if ValidTarget(sid) {
		t.Errorf("tmux resolves %s to another session, but ValidTarget(%q) is true", exactSession(sid), sid)
	}
	if err := New(ctx, "$named", os.TempDir(), "sleep 300"); err == nil {
		if ok, _ := Has(ctx, "$named"); ok {
			t.Error(`tmux found "$named" by name; the session-ID rule may be stale`)
		}
	}

	// "%<n>" / "@<n>": no ID lookup in the exact form...
	for _, id := range []string{pid, wid} {
		if ok, err := Has(ctx, id); err != nil || ok {
			t.Errorf("Has(%q) = %v, %v: the exact target reached c-victim through its pane/window ID", id, ok, err)
		}
		if !ValidTarget(id) {
			t.Errorf("ValidTarget(%q) = false, but tmux doesn't read it as an ID in an exact target", id)
		}
	}
	// ...while a bare target is the pane's / window's session, which is
	// why ccmux never sends one.
	for _, id := range []string{pid, wid} {
		if got := sessionAt(id); got != "c-victim" {
			t.Errorf("bare target %s resolves to %q, want c-victim (the session holding it)", id, got)
		}
	}
	// ...and a session named like an ID is found by that name, and only it.
	for _, id := range []string{pid, wid} {
		if err := New(ctx, id, os.TempDir(), "sleep 300"); err != nil {
			t.Fatal(err)
		}
		if got := sessionAt(exactSession(id)); got != id {
			t.Errorf("%s resolves to %q, want the session named %q", exactSession(id), got, id)
		}
		if err := Kill(ctx, id); err != nil {
			t.Fatalf("Kill(%q): %v", id, err)
		}
		if ok, _ := Has(ctx, "c-victim"); !ok {
			t.Fatalf("Kill(%q) killed c-victim", id)
		}
	}
}
