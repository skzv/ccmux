//go:build darwin || linux

package muse

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt in locally after installing Muse. Ordinary/CI tests use sanitized fixtures.
// This exercises the native writer, exact resume, export, and live lock without
// credentials or a paid model request: Muse provides its own echo backend.
func TestInstalledMuseLifecycle(t *testing.T) {
	if os.Getenv("CCMUX_NATIVE_MUSE") != "1" {
		t.Skip("set CCMUX_NATIVE_MUSE=1 to test the installed Muse CLI")
	}
	binary, err := exec.LookPath("muse")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, name := range []string{"data", "config", "runtime", "cache", "workspace"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for key, dir := range map[string]string{"XDG_DATA_HOME": "data", "XDG_CONFIG_HOME": "config", "XDG_RUNTIME_DIR": "runtime", "XDG_CACHE_HOME": "cache"} {
		t.Setenv(key, filepath.Join(root, dir))
	}
	workspace, err := filepath.EvalSymlinks(filepath.Join(root, "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "exec", "--provider", "echo", "--workspace", workspace, "--disable-write", "--disable-shell", "--disable-web-tools", "--no-foreign-personal-context", "--json", "Native ccmux lifecycle test.")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native exec: %v: %s", err, output)
	}
	sessions, err := List(root)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%d: %v", len(sessions), err)
	}
	s := sessions[0]
	if s.Workspace != workspace || len(s.Messages) != 2 || s.Messages[1].Content != "echo: Native ccmux lifecycle test." || len(s.Requests) != 1 {
		t.Fatalf("native history: %+v", s)
	}
	exported := filepath.Join(root, "export.json")
	if output, err := exec.CommandContext(ctx, binary, "export", "--session", s.Path, "--out", exported).CombinedOutput(); err != nil {
		t.Fatalf("native export: %v: %s", err, output)
	}
	var export struct {
		Sessions []struct {
			ID    string `json:"session_id"`
			Turns int    `json:"turn_count"`
		} `json:"sessions"`
	}
	data, err := os.ReadFile(exported)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &export); err != nil {
		t.Fatal(err)
	}
	if len(export.Sessions) != 1 || export.Sessions[0].ID != s.ID || export.Sessions[0].Turns != 1 {
		t.Fatalf("export mismatch: %+v", export)
	}
	// A private tmux server provides a real terminal, including Muse's cursor
	// position queries, without sharing any of the user's existing sessions.
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("ccmux-muse-test-%d", time.Now().UnixNano())
	run := func(args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, tmux, append([]string{"-L", socket}, args...)...).CombinedOutput()
	}
	defer func() {
		_, _ = exec.CommandContext(context.Background(), tmux, "-L", socket, "kill-server").CombinedOutput()
	}()
	args := []string{"new-session", "-d", "-s", "probe", "-x", "110", "-y", "32", "-c", workspace, binary, "resume", s.ID, "--provider", "echo", "--workspace", workspace, "--trust-workspace", "--disable-write", "--disable-shell"}
	if output, err := run(args...); err != nil {
		t.Fatalf("native resume: %v: %s", err, output)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		pane, err := run("capture-pane", "-p", "-t", "probe")
		if err == nil && (strings.Contains(string(pane), "Type @ to search") || strings.Contains(string(pane), "\n⟩\n")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resume did not show input: %v: %s", err, pane)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := Delete(root, s.ID, s.Path); err == nil {
		t.Fatal("deleted running native Muse")
	}
	if output, err := run("send-keys", "-t", "probe", "-l", "Continue this exact session."); err != nil {
		t.Fatalf("type: %v: %s", err, output)
	}
	// Keep paste and submit separate: Muse handles pasted newlines as input.
	time.Sleep(time.Second)
	if output, err := run("send-keys", "-t", "probe", "Enter"); err != nil {
		t.Fatalf("submit: %v: %s", err, output)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		current, err := Read(s.Path)
		if err == nil && len(current.Messages) == 4 && current.Messages[3].Content == "echo: Continue this exact session." {
			break
		}
		if time.Now().After(deadline) {
			pane, _ := run("capture-pane", "-p", "-t", "probe")
			t.Fatalf("resumed history: %+v, %v: %s", current.Messages, err, pane)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if output, err := run("kill-session", "-t", "probe"); err != nil {
		t.Fatalf("stop: %v: %s", err, output)
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
		err := Delete(root, s.ID, s.Path)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Dir(s.Path)); !os.IsNotExist(err) {
		t.Fatalf("native session retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(SessionsRoot(root), ".msp-view-v1", s.ID)); !os.IsNotExist(err) {
		t.Fatalf("native view cache retained: %v", err)
	}
}
