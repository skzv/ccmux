package sshsetup

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestTargetAddr_BracketsIPv6(t *testing.T) {
	got := (Target{Host: "fd7a:115c:a1e0::1", Port: 2222}).Addr()
	if got != "[fd7a:115c:a1e0::1]:2222" {
		t.Errorf("Addr = %q, want [fd7a:115c:a1e0::1]:2222", got)
	}
}

func TestAppendKnownHost_PrependsNewlineWhenMissing(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(p, []byte("existing-host ssh-ed25519 AAAA"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appendKnownHost(p, "mini", sshPub); err != nil {
		t.Fatalf("appendKnownHost: %v", err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 separate lines, got %d:\n%q", len(lines), string(raw))
	}
	if !strings.HasPrefix(lines[1], "mini ") {
		t.Errorf("second line = %q, want prefix 'mini '", lines[1])
	}
}
