package sshsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestEnsureLocalKey_GeneratesEd25519WhenAbsent — first-run case: no
// ~/.ssh keys, EnsureLocalKey creates a passphrase-less ed25519 pair
// in a freshly-isolated HOME. The whole installer hinges on this so
// it's the highest-priority happy path.
func TestEnsureLocalKey_GeneratesEd25519WhenAbsent(t *testing.T) {
	withTempHome(t)
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatalf("EnsureLocalKey: %v", err)
	}
	if !strings.HasSuffix(lk.PrivatePath, "id_ed25519") {
		t.Errorf("PrivatePath = %q, want suffix id_ed25519", lk.PrivatePath)
	}
	if !strings.HasSuffix(lk.PublicPath, ".pub") {
		t.Errorf("PublicPath = %q, want .pub suffix", lk.PublicPath)
	}
	if !strings.HasPrefix(lk.PublicLine, "ssh-ed25519 ") {
		t.Errorf("PublicLine = %q, want ssh-ed25519 prefix", lk.PublicLine)
	}
	// Private file must be 0600 — a 644 here would be a quiet
	// security regression that openssh would later refuse to load.
	st, err := os.Stat(lk.PrivatePath)
	if err != nil {
		t.Fatalf("stat private: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Errorf("private key mode = %o, want 0600", mode)
	}
	// And the key must actually load through ssh.ParsePrivateKey —
	// catches a bad PEM encoding regression even if the file looks
	// right by mode/size.
	priv, err := os.ReadFile(lk.PrivatePath)
	if err != nil {
		t.Fatalf("read private: %v", err)
	}
	if _, err := ssh.ParsePrivateKey(priv); err != nil {
		t.Fatalf("generated private key does not parse: %v", err)
	}
}

// TestEnsureLocalKey_ReusesExistingEd25519 — second-run case: an
// id_ed25519 already exists, EnsureLocalKey must return its paths
// and the existing public line, NOT generate a new key. The whole
// "don't blow away the user's existing setup" promise hangs on this.
func TestEnsureLocalKey_ReusesExistingEd25519(t *testing.T) {
	home := withTempHome(t)
	// Plant a fake but VALID ed25519 keypair so EnsureLocalKey can
	// parse the .pub through the sanity-check path.
	planted := generateAndPlant(t, home, "id_ed25519")
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatalf("EnsureLocalKey: %v", err)
	}
	if lk.PrivatePath != planted.PrivatePath {
		t.Errorf("reused wrong key: got %q, want %q", lk.PrivatePath, planted.PrivatePath)
	}
	if lk.PublicLine != planted.PublicLine {
		t.Errorf("returned PublicLine doesn't match planted:\n  got:  %q\n  want: %q",
			lk.PublicLine, planted.PublicLine)
	}
}

// TestEnsureLocalKey_PrefersEd25519OverRSA — when both keys exist,
// ed25519 wins. Modern algorithm by default, no exception. RSA is
// only used as a fallback when ed25519 is absent.
func TestEnsureLocalKey_PrefersEd25519OverRSA(t *testing.T) {
	home := withTempHome(t)
	rsaPlanted := generateAndPlant(t, home, "id_rsa")
	edPlanted := generateAndPlant(t, home, "id_ed25519")
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatalf("EnsureLocalKey: %v", err)
	}
	if lk.PrivatePath != edPlanted.PrivatePath {
		t.Errorf("picked %q (rsa: %q, ed25519: %q); ed25519 must win",
			lk.PrivatePath, rsaPlanted.PrivatePath, edPlanted.PrivatePath)
	}
}

// TestEnsureLocalKey_FallsBackToRSAWhenEd25519Missing — the
// reasonable middle path. User has only an RSA key (common on older
// dev boxes), we reuse it.
func TestEnsureLocalKey_FallsBackToRSAWhenEd25519Missing(t *testing.T) {
	home := withTempHome(t)
	rsaPlanted := generateAndPlant(t, home, "id_rsa")
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatalf("EnsureLocalKey: %v", err)
	}
	if lk.PrivatePath != rsaPlanted.PrivatePath {
		t.Errorf("expected RSA fallback, got %q", lk.PrivatePath)
	}
}

// TestEnsureLocalKey_RejectsCorruptPublic — defensive: if the .pub
// file exists but doesn't parse, EnsureLocalKey must NOT reuse the
// corrupt line. The bug we're guarding against is "silently
// installed gibberish on the remote because the .pub was corrupt".
//
// Either outcome is acceptable: (a) fall through to RSA, (b) error
// out with a clear message. What is NOT acceptable: return the
// corrupt line in PublicLine and proceed. The assertion pins exactly
// that invariant.
func TestEnsureLocalKey_RejectsCorruptPublic(t *testing.T) {
	home := withTempHome(t)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	priv := filepath.Join(sshDir, "id_ed25519")
	pub := priv + ".pub"
	if err := os.WriteFile(priv, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\ngarbage\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pub, []byte("not an ssh key"), 0o644); err != nil {
		t.Fatal(err)
	}
	lk, err := EnsureLocalKey()
	// Two valid outcomes:
	//   1. err != nil  (we couldn't safely produce a key — refuse)
	//   2. err == nil && PublicLine != "not an ssh key" (regenerated)
	// Forbidden: err == nil && PublicLine == corrupt.
	if err == nil && lk.PublicLine == "not an ssh key" {
		t.Fatalf("EnsureLocalKey returned the corrupt public line: %q", lk.PublicLine)
	}
}

// TestMiddleField_HandlesCommonShapes pins the dedup helper. The
// idempotency check in remoteInstallKey grep's for this middle
// field, so it has to be exactly the base64 chunk — no algorithm
// prefix, no comment.
func TestMiddleField_HandlesCommonShapes(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAbcDef= user@host", "AAAAC3NzaC1lZDI1NTE5AAAAIAbcDef="},
		{"ssh-rsa AAAAB3NzaC1y= comment with spaces", "AAAAB3NzaC1y="},
		{"only-one-field", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := middleField(c.in)
		if got != c.want {
			t.Errorf("middleField(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestShellQuote_EscapesSingleQuotes — the script writes the key
// inside single quotes; a malicious or just-weird comment field
// containing a quote could otherwise break out of the literal.
func TestShellQuote_EscapesSingleQuotes(t *testing.T) {
	got := shellQuote("hello 'world'")
	want := `'hello '"'"'world'"'"''`
	if got != want {
		t.Errorf("shellQuote = %q, want %q", got, want)
	}
}

// TestParseDscl_FiltersSystemAccounts pins the macOS user-enum
// path. _amavisd (UID 284) gets dropped, skz (UID 501) survives.
func TestParseDscl_FiltersSystemAccounts(t *testing.T) {
	input := `root             0
_amavisd       284
_atsserver     97
skz            501
alice          502
`
	got := parseDscl(input)
	want := "skz alice"
	if got != want {
		t.Errorf("parseDscl = %q, want %q", got, want)
	}
}

// TestParseEtcPasswd_FiltersSystemAndDisabled — Linux side: UID < 1000 out,
// disabled shells out, real users kept.
func TestParseEtcPasswd_FiltersSystemAndDisabled(t *testing.T) {
	input := `root:x:0:0:root:/root:/bin/bash
daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
alice:x:1001:1001:Alice:/home/alice:/bin/bash
bob:x:1002:1002:Bob:/home/bob:/bin/zsh
disabled:x:1003:1003:Disabled:/home/disabled:/usr/sbin/nologin
`
	got := parseEtcPasswd(input)
	want := "alice bob"
	if got != want {
		t.Errorf("parseEtcPasswd = %q, want %q", got, want)
	}
}

// TestProbeResult_IsSetupNeeded — only AuthFailed routes to the
// wizard. Other states need different remediation (no point
// running the wizard for a host that's offline).
func TestProbeResult_IsSetupNeeded(t *testing.T) {
	cases := []struct {
		in   ProbeResult
		want bool
	}{
		{ProbeOK, false},
		{ProbeAuthFailed, true},
		{ProbeSshdDisabled, false},
		{ProbeRefused, false},
		{ProbeTimeout, false},
		{ProbeHostKeyMismatch, false},
		{ProbeNoNetwork, false},
		{ProbeUnknown, false},
	}
	for _, c := range cases {
		if got := c.in.IsSetupNeeded(); got != c.want {
			t.Errorf("%v.IsSetupNeeded() = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestProbeResult_StringStable — log/test stability. The string
// tokens are part of the package's contract (used in structured
// logs and test assertions); changing them is a breaking change.
func TestProbeResult_StringStable(t *testing.T) {
	cases := map[ProbeResult]string{
		ProbeOK:              "ok",
		ProbeAuthFailed:      "auth-failed",
		ProbeSshdDisabled:    "sshd-disabled",
		ProbeRefused:         "refused",
		ProbeTimeout:         "timeout",
		ProbeHostKeyMismatch: "host-key-mismatch",
		ProbeNoNetwork:       "no-network",
		ProbeUnknown:         "unknown",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("%v.String() = %q, want %q", int(r), got, want)
		}
	}
}

// TestTarget_String_FormatsCleanly — the wizard banner shows this
// string verbatim. user@host is the canonical form; non-22 ports
// get an explicit suffix.
func TestTarget_String_FormatsCleanly(t *testing.T) {
	cases := []struct {
		in   Target
		want string
	}{
		{Target{User: "alice", Host: "sputnik"}, "alice@sputnik"},
		{Target{User: "alice", Host: "sputnik", Port: 22}, "alice@sputnik"},
		{Target{User: "alice", Host: "sputnik", Port: 2222}, "alice@sputnik:2222"},
		{Target{Host: "sputnik"}, "<user>@sputnik"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("Target%+v.String() = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestProgress_NilSafe — wizard passes nil for Progress in some
// paths (`ccmux doctor`); the helper must not panic.
func TestProgress_NilSafe(t *testing.T) {
	var p Progress
	p.report("stage", "detail") // must not panic
}
