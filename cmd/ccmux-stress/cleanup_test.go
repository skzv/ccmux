//go:build !windows

package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
)

// stressSandbox isolates a profile run: $HOME is a short temp dir with
// no ccmuxd socket, PATH holds only a fake tmux that logs every call
// (so no real tmux server is touched), and reports go to a temp dir.
// It returns the fake tmux's log path.
func stressSandbox(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "cxs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(home, "tmux.log")
	fake := "#!/bin/sh\nprintf '%s|' \"$@\" >> '" + log + "'\nprintf '\\n' >> '" + log + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	prev := reportDirFlag
	reportDirFlag = t.TempDir()
	t.Cleanup(func() { reportDirFlag = prev })
	return log
}

// assertAllKilled checks the fake tmux saw an exact-target
// kill-session for every session it was asked to create.
func assertAllKilled(t *testing.T, log string) {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var created, killed []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Split(line, "|")
		switch f[0] {
		case "new-session":
			for i, a := range f {
				if a == "-s" && i+1 < len(f) {
					created = append(created, f[i+1])
				}
			}
		case "kill-session":
			if len(f) > 2 {
				killed = append(killed, f[2])
			}
		}
	}
	if len(created) == 0 {
		t.Fatalf("no sessions were created; tmux log:\n%s", raw)
	}
	for _, name := range created {
		found := false
		for _, k := range killed {
			if k == "="+name {
				found = true
			}
		}
		if !found {
			t.Errorf("session %s was never killed (want kill-session -t =%s); tmux log:\n%s", name, name, raw)
		}
	}
}

// TestRunSessions_KillsSpawnedSessions — `defer cleanupSessions(spawned)`
// evaluated the (empty) slice at defer time, so every session leaked.
// Here the run stops early (no daemon to measure) and must still kill
// what it spawned.
func TestRunSessions_KillsSpawnedSessions(t *testing.T) {
	log := stressSandbox(t)
	if err := runSessions(context.Background(), 2, 100*time.Millisecond, 50*time.Millisecond); err == nil {
		t.Error("with no daemon to measure, the sessions profile should fail loudly")
	}
	assertAllKilled(t, log)
}

// TestRunLonghaul_KillsSpawnedSessions — same leak in longhaul.
func TestRunLonghaul_KillsSpawnedSessions(t *testing.T) {
	log := stressSandbox(t)
	if code, _ := runLonghaul(context.Background(), time.Second, 2, time.Second); code == 0 {
		t.Error("with no daemon to measure, longhaul should exit non-zero")
	}
	assertAllKilled(t, log)
}

// TestRunNotifications_KillsSpawnedSessions — same leak in
// notifications; its panes are respawned into the v2 input box.
func TestRunNotifications_KillsSpawnedSessions(t *testing.T) {
	log := stressSandbox(t)
	if err := runNotifications(context.Background(), 2, 10*time.Millisecond, 100*time.Millisecond); err != nil {
		t.Fatalf("notifications: %v", err)
	}
	assertAllKilled(t, log)
	raw, _ := os.ReadFile(log)
	if !strings.Contains(string(raw), "respawn-pane") || !strings.Contains(string(raw), "❯") {
		t.Errorf("panes should be respawned into the v2 input box; tmux log:\n%s", raw)
	}
}

// TestNotificationPayloadIsNeedsInput — the old payload
// (`│ > waiting for input │`, Claude Code v1's frame) no longer
// classified as needs_input once detection moved to the v2 shapes, so
// the profile measured nothing. What the script prints must classify
// as needs_input with Claude's rules once the pane is quiet.
func TestNotificationPayloadIsNeedsInput(t *testing.T) {
	printPart := strings.TrimSuffix(claudePromptScript, " ; sleep 9999")
	if printPart == claudePromptScript {
		t.Fatalf("unexpected script shape: %q", claudePromptScript)
	}
	out, err := exec.Command("/bin/sh", "-c", printPart).Output()
	if err != nil {
		t.Fatalf("run %q: %v", printPart, err)
	}
	if string(out) != claudeV2InputBox {
		t.Fatalf("script prints %q, want %q", out, claudeV2InputBox)
	}
	quiet := time.Now().Add(-10 * time.Minute)
	if got := agent.ClassifyState(agent.ByID(agent.IDClaude), string(out), "", quiet, 3*time.Second); got != agent.StateNeedsInput {
		t.Errorf("payload classifies as %q, want %q", got, agent.StateNeedsInput)
	}
}

// TestFindCcmuxd_ReadsSocketPeer — the daemon pid came from the first
// `pgrep -x ccmuxd`: any user's, any sandbox's. It must be the process
// behind the socket this run's client talks to — here, the test
// itself.
func TestFindCcmuxd_ReadsSocketPeer(t *testing.T) {
	stressSandbox(t)
	home, _ := os.UserHomeDir()
	sockDir := filepath.Join(home, ".local", "state", "ccmux")
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(sockDir, "ccmuxd.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	pid, err := findCcmuxd()
	if err != nil {
		t.Fatalf("findCcmuxd: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("findCcmuxd = %d, want this process (%d), the socket's listener", pid, os.Getpid())
	}
}

// TestFindCcmuxd_NoDaemonFailsLoudly — no socket means no daemon to
// measure: an error naming the socket, not pid 0 and a quiet skip.
func TestFindCcmuxd_NoDaemonFailsLoudly(t *testing.T) {
	stressSandbox(t)
	if pid, err := findCcmuxd(); err == nil || !strings.Contains(err.Error(), "ccmuxd.sock") {
		t.Errorf("findCcmuxd = %d, %v; want an error naming the socket", pid, err)
	}
}

// TestReportDir — reports go to --report-dir, else docs/03_Agent_Logs in
// a checkout, else the temp dir (the help used to promise
// docs/03_Agent_Logs unconditionally).
func TestReportDir(t *testing.T) {
	prev := reportDirFlag
	t.Cleanup(func() { reportDirFlag = prev })

	reportDirFlag = ""
	t.Chdir(t.TempDir())
	if got := reportDir(); got != os.TempDir() {
		t.Errorf("outside a checkout: reportDir = %q, want %q", got, os.TempDir())
	}
	if err := os.MkdirAll(agentLogsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := reportDir(); got != agentLogsDir {
		t.Errorf("in a checkout: reportDir = %q, want %q", got, agentLogsDir)
	}
	reportDirFlag = "/some/where"
	if got := reportDir(); got != "/some/where" {
		t.Errorf("--report-dir: reportDir = %q", got)
	}
	help := rootCmd.Long
	if !strings.Contains(help, "system temp dir") || !strings.Contains(help, "--report-dir") {
		t.Errorf("root help should describe where reports go:\n%s", help)
	}
}
