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
