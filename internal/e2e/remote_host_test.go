//go:build integration

package e2e

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/config"
)

// peerMachine is a second ccmux machine for a test: its own $HOME, its
// own tmux server, and a ccmuxd whose tailnet listener serves on
// 127.0.0.1:<port> (a stub `tailscale ip -4` answers 127.0.0.1) — what
// a configured remote host looks like to this machine's CLI.
type peerMachine struct {
	home string
	addr string
	env  []string
}

// startPeerMachine starts the peer's ccmuxd and waits for its tailnet
// listener to answer. Its daemon and tmux server are stopped, and its
// sandbox removed, when the test ends.
func startPeerMachine(t *testing.T) *peerMachine {
	t.Helper()
	home := shortTempDir(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	bin := filepath.Join(home, "bin")
	mkdirAll(t, bin)
	writeFile(t, filepath.Join(bin, "tailscale"), "#!/bin/sh\necho 127.0.0.1\n")
	if err := os.Chmod(filepath.Join(bin, "tailscale"), 0o755); err != nil {
		t.Fatal(err)
	}
	mkdirAll(t, filepath.Join(home, "Projects"))
	writeFile(t, filepath.Join(home, ".config", "ccmux", "config.toml"), fmt.Sprintf(
		"schema_version = %d\n[projects]\nroot = %q\n[daemon]\nlisten_tailnet = true\ntailnet_port = %d\npoll_interval_seconds = 1\n",
		config.SchemaVersion, filepath.Join(home, "Projects"), port))

	p := &peerMachine{home: home, addr: fmt.Sprintf("127.0.0.1:%d", port)}
	p.env = envWithEnglish("HOME="+home, "TMUX_TMPDIR="+home, "TMUX=", "PATH="+bin+string(os.PathListSeparator)+e2ePath())

	cmd := exec.Command("sh", "-c", daemonWatchdog, builtCcmuxd)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Dir = home
	cmd.Env = p.env
	log := &safeBuffer{}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start peer ccmuxd: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _, _ = cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
		_, _ = p.tmux("kill-server")
	})
	if !waitFor(10*time.Second, func() bool {
		resp, err := http.Get("http://" + p.addr + "/v1/health")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}) {
		t.Fatalf("peer ccmuxd's tailnet listener never answered on %s; log:\n%s", p.addr, log.String())
	}
	return p
}

// tmux runs tmux against the peer's server.
func (p *peerMachine) tmux(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "tmux", args...)
	c.Env = p.env
	out, err := c.CombinedOutput()
	return string(out), err
}

// sessions lists the peer's sessions by name.
func (p *peerMachine) sessions() []string {
	out, err := p.tmux("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

// TestHostFlag_KillRenameListAcrossMachines — `ccmux kill` and `ccmux
// rename` could only act on this machine's tmux. With --host they go to
// a configured host's ccmuxd: here a second, real daemon with its own
// tmux server, reached over its tailnet listener. The same-named local
// session is never touched.
func TestHostFlag_KillRenameListAcrossMachines(t *testing.T) {
	e := newEnv(t)
	peer := startPeerMachine(t)
	if out, err := peer.tmux("new-session", "-d", "-s", "work", "-c", peer.home); err != nil {
		t.Fatalf("peer session: %v\n%s", err, out)
	}
	e.newTmuxSession("work", e.Home) // this machine's own "work"
	host, portStr, _ := net.SplitHostPort(peer.addr)
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	cfg := e.defaultConfig()
	cfg.Hosts = []config.Host{{Name: "box", Address: host, Port: port}}
	e.writeConfig(cfg)

	stdout, stderr, err := e.ccmux("list", "--host", "box")
	if err != nil || !strings.Contains(stdout, "work") || !strings.Contains(stdout, "box") {
		t.Fatalf("list --host box: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	stdout, stderr, err = e.ccmux("rename", "--host", "box", "work", "work2")
	if err != nil || !strings.Contains(stdout, "renamed work → work2 on box") {
		t.Fatalf("rename --host: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if got := peer.sessions(); len(got) != 1 || got[0] != "work2" {
		t.Errorf("peer sessions after rename = %v, want [work2]", got)
	}
	if !e.hasSession("work") || e.hasSession("work2") {
		t.Errorf("rename --host touched this machine's sessions: %v", e.sessionNames())
	}

	// A name clash on the peer is reported as such.
	if out, err := peer.tmux("new-session", "-d", "-s", "other", "-c", peer.home); err != nil {
		t.Fatalf("peer session: %v\n%s", err, out)
	}
	_, stderr, err = e.ccmux("rename", "--host", "box", "other", "work2")
	if err == nil || !strings.Contains(stderr, `box already has a session named "work2"`) {
		t.Errorf("rename onto a taken name: err %v, stderr %s", err, stderr)
	}

	stdout, stderr, err = e.ccmux("kill", "--host", "box", "work2")
	if err != nil || !strings.Contains(stdout, "killed work2 on box") {
		t.Fatalf("kill --host: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if got := peer.sessions(); len(got) != 1 || got[0] != "other" {
		t.Errorf("peer sessions after kill = %v, want [other]", got)
	}
	if !e.hasSession("work") {
		t.Error("kill --host killed this machine's work")
	}
	_, stderr, err = e.ccmux("kill", "--host", "box", "work2")
	if err == nil || !strings.Contains(stderr, "host box") {
		t.Errorf("killing it again: err %v, stderr %s", err, stderr)
	}

	// The daemon side of `attach --host`: a project on the peer gets its
	// session from the peer's daemon (the ssh leg needs a terminal).
	mkdirAll(t, filepath.Join(peer.home, "Projects", "api"))
	stdout, stderr, err = e.ccmux("attach", "--host", "box", "api")
	if err != nil || !strings.Contains(stdout, "c-api started on box") || !strings.Contains(stdout, "ccmux attach --host box c-api") {
		t.Errorf("attach --host box api without a terminal: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if !strings.Contains(strings.Join(peer.sessions(), " "), "c-api") {
		t.Errorf("peer sessions = %v, want c-api started there", peer.sessions())
	}
	if e.hasSession("c-api") {
		t.Error("attach --host started the session on this machine")
	}
}
