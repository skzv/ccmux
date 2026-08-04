//go:build integration

package e2e

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// TestHostAdd_RejectsDuplicateName pins the fix from #172: `ccmux host
// add` with an already-configured name must fail (non-zero exit, error
// naming the conflict) instead of silently appending a second entry —
// `host remove <name>` deletes every entry with that name, so a
// duplicate add would make the eventual remove wipe both.
func TestHostAdd_RejectsDuplicateName(t *testing.T) {
	e := newEnv(t)

	if _, stderr, err := e.ccmux("host", "add", "boxdup", "100.64.0.1"); err != nil {
		t.Fatalf("first host add: %v\nstderr: %s", err, stderr)
	}

	_, stderr, err := e.ccmux("host", "add", "boxdup", "100.64.0.2")
	if err == nil {
		t.Fatal("duplicate `host add boxdup` unexpectedly succeeded")
	}
	if !strings.Contains(stderr, "boxdup") {
		t.Errorf("duplicate-add error does not name the conflicting host; stderr: %q", stderr)
	}

	// Exactly ONE entry — with the ORIGINAL address — must survive.
	cfg, cerr := config.Load()
	if cerr != nil {
		t.Fatalf("config.Load after duplicate add: %v", cerr)
	}
	var matches []config.Host
	for _, h := range cfg.Hosts {
		if h.Name == "boxdup" {
			matches = append(matches, h)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("config has %d hosts named boxdup, want exactly 1: %+v", len(matches), matches)
	}
	if matches[0].Address != "100.64.0.1" {
		t.Errorf("surviving boxdup address = %q, want the original 100.64.0.1", matches[0].Address)
	}

	// And `host list` shows exactly one row for it.
	stdout, _, lerr := e.ccmux("host", "list")
	if lerr != nil {
		t.Fatalf("host list: %v", lerr)
	}
	rows := 0
	for _, ln := range strings.Split(stdout, "\n") {
		if strings.Contains(ln, "boxdup") {
			rows++
		}
	}
	if rows != 1 {
		t.Errorf("host list shows %d boxdup rows, want exactly 1:\n%s", rows, stdout)
	}
}

// TestConfigFile_Mode0600AndRewriteKeepsBothChanges pins the config
// persistence fix from #169: config.toml used to be written 0644 (it
// carries secrets — API keys) and non-atomically (os.Create truncated
// before encoding a byte). After a CLI action that saves config the
// file must be 0600, and a second config-mutating command must leave a
// valid TOML file containing BOTH changes — no truncation, no lost
// fields.
func TestConfigFile_Mode0600AndRewriteKeepsBothChanges(t *testing.T) {
	e := newEnv(t)

	p, err := config.Path()
	if err != nil {
		t.Fatalf("config.Path: %v", err)
	}

	if _, stderr, err := e.ccmux("host", "add", "modeboxA", "100.64.0.10"); err != nil {
		t.Fatalf("host add modeboxA: %v\nstderr: %s", err, stderr)
	}
	assertConfigMode0600(t, p)

	// A second mutating command rewrites the file; both changes must
	// survive the round-trip.
	if _, stderr, err := e.ccmux("host", "add", "modeboxB", "100.64.0.11"); err != nil {
		t.Fatalf("host add modeboxB: %v\nstderr: %s", err, stderr)
	}
	assertConfigMode0600(t, p)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.toml no longer parses after rewrite: %v", err)
	}
	for _, want := range []struct{ name, addr string }{
		{"modeboxA", "100.64.0.10"},
		{"modeboxB", "100.64.0.11"},
	} {
		found := false
		for _, h := range cfg.Hosts {
			if h.Name == want.name && h.Address == want.addr {
				found = true
			}
		}
		if !found {
			t.Errorf("host %s (%s) missing after rewrite; hosts = %+v", want.name, want.addr, cfg.Hosts)
		}
	}
	// Pre-existing content (the fixture's projects root) must also
	// survive — a truncated or defaults-reset file would lose it.
	if cfg.Projects.Root != e.Root {
		t.Errorf("projects root = %q after rewrites, want %q — pre-existing config was lost", cfg.Projects.Root, e.Root)
	}
}

// assertConfigMode0600 checks the config file's permission bits.
// Skipped on Windows, where POSIX permission bits aren't meaningful.
func assertConfigMode0600(t *testing.T, p string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat %s: %v", p, err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("config.toml mode = %04o, want 0600 (file carries secrets)", got)
	}
}

// TestDoctor_HonorsConfiguredSSHPort pins the fix from #169: `ccmux
// doctor` used to hardcode port 22 when probing configured hosts,
// misreporting any host with a custom ssh_port. With a host whose
// ssh_port points at a closed loopback port, the failure line must
// name the CONFIGURED port, not 22.
func TestDoctor_HonorsConfiguredSSHPort(t *testing.T) {
	e := newEnv(t)

	// Reserve a loopback port then close it so nothing listens: the
	// probe gets an instant ECONNREFUSED — fast and offline-safe.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	cfg := e.defaultConfig()
	cfg.Hosts = append(cfg.Hosts, config.Host{
		Name: "portbox", Address: "127.0.0.1", Port: 7474, SSHPort: port, Mosh: true,
	})
	e.writeConfig(cfg)

	// doctor os.Exit(n)s when n tools are missing (mosh/tailscale may
	// be absent in CI) — run via subprocess, read stdout regardless.
	stdout, _, _ := e.ccmux("doctor")
	if !strings.Contains(stdout, "portbox") {
		t.Fatalf("doctor did not report the configured host portbox; got:\n%s", stdout)
	}
	// The refused-connection line reads "port <n> on 127.0.0.1 closed"
	// — it must reference the configured port.
	if !strings.Contains(stdout, fmt.Sprintf("port %d", port)) {
		t.Errorf("doctor output does not reference configured ssh_port %d; got:\n%s", port, stdout)
	}
	if strings.Contains(stdout, "port 22 on") {
		t.Errorf("doctor probed hardcoded port 22 instead of the configured ssh_port; got:\n%s", stdout)
	}
}
