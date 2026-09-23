package sshsetup

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// The scenario these tests pin is the stock first-time `setup-ssh` run:
//
//  1. Probe shells out to openssh with StrictHostKeyChecking=accept-new.
//     openssh prefers ed25519, so known_hosts gains ONLY the host's
//     ed25519 key (auth then fails — that's why the wizard runs).
//  2. The install hop dials with x/crypto/ssh. Its default host-key
//     order prefers ECDSA, so a stock sshd (ed25519 + ecdsa host keys)
//     presented its ECDSA key, knownhosts saw a known host with an
//     unrecorded key type, and the wizard reported "host key mismatch /
//     possible MITM" — for every user, against every stock sshd.
//
// The servers below are real in-process x/crypto SSH servers, and the
// installer/enumerator use the production dial + host-key policy.

func genECDSAHostSigner(t *testing.T) ssh.Signer {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// stockSshd is an in-process server offering both an ed25519 and an
// ECDSA host key, like a default openssh-server install.
func stockSshd(t *testing.T) (*testServer, ssh.Signer) {
	t.Helper()
	ecdsaKey := genECDSAHostSigner(t)
	return newTestServerWithHostKeys(t, ecdsaKey), ecdsaKey
}

func writeKnownHosts(t *testing.T, home string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "known_hosts")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func targetFor(srv *testServer) Target {
	return Target{User: "alice", Host: srv.Host(), Port: srv.Port()}
}

// TestInstall_OnlyEd25519RecordedAgainstStockSshd — the exact failing
// onboarding scenario: known_hosts holds only the ed25519 key (what
// Probe's openssh recorded) and the server offers ed25519 AND ecdsa.
// Install must verify against the recorded key and succeed, and must
// not append a second (ecdsa) entry.
func TestInstall_OnlyEd25519RecordedAgainstStockSshd(t *testing.T) {
	srv, _ := stockSshd(t)
	home := withTempHome(t)
	kh := writeKnownHosts(t, home, srv.HostKeyLine())
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatal(err)
	}

	err = defaultInstaller().Install(context.Background(), targetFor(srv), srv.password, lk, nil)
	if err != nil {
		t.Fatalf("Install against a stock sshd with its ed25519 key recorded: %v", err)
	}
	if got := readFile(t, kh); got != srv.HostKeyLine()+"\n" {
		t.Errorf("known_hosts changed; want only the recorded ed25519 line, got:\n%s", got)
	}
}

// TestEnumerate_OnlyEd25519RecordedAgainstStockSshd — the enumerate
// hop (run right after install) dials the same way and hit the same
// false mismatch.
func TestEnumerate_OnlyEd25519RecordedAgainstStockSshd(t *testing.T) {
	srv, _ := stockSshd(t)
	home := withTempHome(t)
	writeKnownHosts(t, home, srv.HostKeyLine())
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatal(err)
	}
	srv.AuthorizeKey(lk.PublicLine)

	users, err := defaultEnumerator().Enumerate(context.Background(), targetFor(srv), lk)
	if err != nil {
		t.Fatalf("Enumerate against a stock sshd with its ed25519 key recorded: %v", err)
	}
	if !reflect.DeepEqual(users, []string{"bob"}) {
		t.Errorf("users = %v, want [bob]", users)
	}
}

// TestInstall_HashedEd25519EntryAgainstStockSshd — Debian/Ubuntu ship
// HashKnownHosts=yes, so Probe's recorded line is hashed. The recorded
// type must still be found.
func TestInstall_HashedEd25519EntryAgainstStockSshd(t *testing.T) {
	srv, _ := stockSshd(t)
	home := withTempHome(t)
	addr := targetFor(srv).Addr()
	hashed := knownhosts.Line([]string{knownhosts.HashHostname(knownhosts.Normalize(addr))}, srv.hostKey.PublicKey())
	writeKnownHosts(t, home, hashed)
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := defaultInstaller().Install(context.Background(), targetFor(srv), srv.password, lk, nil); err != nil {
		t.Fatalf("Install with a hashed ed25519 known_hosts entry: %v", err)
	}
}

// TestInstall_ChangedEd25519KeyIsMismatch — a genuinely changed key
// (same type, different material) must still be refused as a mismatch.
func TestInstall_ChangedEd25519KeyIsMismatch(t *testing.T) {
	srv, _ := stockSshd(t)
	home := withTempHome(t)
	addr := targetFor(srv).Addr()
	stale := knownhosts.Line([]string{addr}, genHostKey(t)) // some other ed25519 key
	kh := writeKnownHosts(t, home, stale)
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatal(err)
	}

	err = defaultInstaller().Install(context.Background(), targetFor(srv), srv.password, lk, nil)
	if !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("err = %v, want ErrHostKeyMismatch for a changed ed25519 key", err)
	}
	if got := readFile(t, kh); got != stale+"\n" {
		t.Errorf("known_hosts must be untouched on a mismatch, got:\n%s", got)
	}
}

// TestInstall_OnlyECDSARecordedAgainstStockSshd — the recorded type
// wins over our own ed25519-first preference.
func TestInstall_OnlyECDSARecordedAgainstStockSshd(t *testing.T) {
	srv, ecdsaKey := stockSshd(t)
	home := withTempHome(t)
	kh := writeKnownHosts(t, home, knownhosts.Line([]string{targetFor(srv).Addr()}, ecdsaKey.PublicKey()))
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := defaultInstaller().Install(context.Background(), targetFor(srv), srv.password, lk, nil); err != nil {
		t.Fatalf("Install with only the ecdsa key recorded: %v", err)
	}
	if got := strings.Count(readFile(t, kh), "\n"); got != 1 {
		t.Errorf("known_hosts has %d lines, want the single recorded ecdsa line", got)
	}
}

// TestInstall_FirstContactRecordsEd25519 — on a host we've never seen,
// TOFU must record the same key type openssh would (ed25519), so the
// later openssh/mosh attach verifies against what we pinned. x/crypto's
// default order recorded the ECDSA key instead.
func TestInstall_FirstContactRecordsEd25519(t *testing.T) {
	srv, _ := stockSshd(t)
	home := withTempHome(t)
	kh := writeKnownHosts(t, home) // empty
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := defaultInstaller().Install(context.Background(), targetFor(srv), srv.password, lk, nil); err != nil {
		t.Fatalf("first-contact Install: %v", err)
	}
	if got, want := readFile(t, kh), srv.HostKeyLine()+"\n"; got != want {
		t.Errorf("known_hosts after TOFU =\n%s\nwant exactly the ed25519 line:\n%s", got, want)
	}
}

// TestInstall_RecordedTypeNotOfferedIsNotMismatch — known_hosts pins an
// ecdsa key, but this server only has ed25519. Nothing recorded was
// contradicted, so this must NOT be reported as a host-key mismatch
// (possible MITM); it is refused with ErrHostKeyTypeNotRecorded.
func TestInstall_RecordedTypeNotOfferedIsNotMismatch(t *testing.T) {
	srv := newTestServer(t) // ed25519 only
	home := withTempHome(t)
	kh := writeKnownHosts(t, home, knownhosts.Line([]string{targetFor(srv).Addr()}, genECDSAHostSigner(t).PublicKey()))
	before := readFile(t, kh)
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatal(err)
	}

	err = defaultInstaller().Install(context.Background(), targetFor(srv), srv.password, lk, nil)
	if errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("err = %v: an unrecorded key TYPE is not a changed key", err)
	}
	if !errors.Is(err, ErrHostKeyTypeNotRecorded) {
		t.Fatalf("err = %v, want ErrHostKeyTypeNotRecorded", err)
	}
	if got := readFile(t, kh); got != before {
		t.Errorf("known_hosts must be untouched, got:\n%s", got)
	}
}

// TestHostKeyAlgorithms_RecordedTypesFirst pins the ordering rule.
func TestHostKeyAlgorithms_RecordedTypesFirst(t *testing.T) {
	cases := []struct {
		recorded  []string
		wantFirst []string
	}{
		{nil, []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256}},
		{[]string{ssh.KeyAlgoECDSA256}, []string{ssh.KeyAlgoECDSA256, ssh.KeyAlgoED25519}},
		{[]string{ssh.KeyAlgoRSA}, []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA, ssh.KeyAlgoED25519}},
		{[]string{ssh.KeyAlgoRSA, ssh.KeyAlgoED25519}, []string{ssh.KeyAlgoED25519, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA, ssh.KeyAlgoECDSA256}},
	}
	for _, tc := range cases {
		got := hostKeyAlgorithms(tc.recorded)
		if len(got) != len(hostKeyPreference) {
			t.Errorf("hostKeyAlgorithms(%v) dropped algorithms: %v", tc.recorded, got)
			continue
		}
		if !reflect.DeepEqual(got[:len(tc.wantFirst)], tc.wantFirst) {
			t.Errorf("hostKeyAlgorithms(%v) = %v, want prefix %v", tc.recorded, got, tc.wantFirst)
		}
	}
}
