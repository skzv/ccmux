package sshsetup

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)

func genHostKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return sshPub
}

// TestTOFU_FreshCallbackCatchesMismatchStaleMisses — regression for the
// stale-host-key-cache bug. knownhosts.New reads known_hosts ONCE into
// an in-memory db, so a callback built before the password hop's TOFU
// append still believes the host is unknown. Install reused one callback
// for both the password and the validation hops, so the validation hop
// re-ran TOFU (accept-any) instead of VERIFYING — a MITM presenting a
// different key on the second hop would be accepted. The fix rebuilds
// the callback between hops. This proves the property the fix relies on:
// a freshly-built callback catches a mismatch that the stale one misses.
func TestTOFU_FreshCallbackCatchesMismatchStaleMisses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	keyA := genHostKey(t)
	keyB := genHostKey(t)
	host := "100.64.0.5:22"
	addr := &net.TCPAddr{IP: net.ParseIP("100.64.0.5"), Port: 22}

	// Password hop: first contact TOFU-accepts keyA and appends it.
	cb1, err := tofuHostKeyCallback()
	if err != nil {
		t.Fatal(err)
	}
	if err := cb1(host, addr, keyA); err != nil {
		t.Fatalf("first contact should TOFU-accept keyA: %v", err)
	}

	// The STALE callback's in-memory db was not refreshed by the disk
	// append, so it still treats the host as unknown and TOFU-accepts a
	// DIFFERENT key — the exact bug the validation hop suffered.
	if err := cb1(host, addr, keyB); err != nil {
		t.Errorf("stale callback unexpectedly rejected keyB (%v); the bug is that it accepts it", err)
	}

	// Validation hop with the FIX: a fresh callback re-reads the updated
	// known_hosts and REJECTS the mismatched key.
	cb2, err := tofuHostKeyCallback()
	if err != nil {
		t.Fatal(err)
	}
	if err := cb2(host, addr, keyB); err == nil {
		t.Error("fresh callback must reject a mismatched host key, got nil (would TOFU re-add)")
	}
	// ...and still accepts the genuinely-recorded key.
	if err := cb2(host, addr, keyA); err != nil {
		t.Errorf("fresh callback should accept the recorded key: %v", err)
	}
}
