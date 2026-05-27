package sshsetup

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestInstallKeyViaPassword_Happy — the headline path: connect with
// password, append the public key, validate by re-connecting with
// key auth. After the call:
//
//   - the server's trust set contains our key's base64 chunk
//   - the script the installer ran on the server matches the
//     expected mkdir/chmod/grep/printf shape
//   - we got no error
//
// Validation reconnect is wired through to the SAME test server,
// which by then has the key in its trust map.
func TestInstallKeyViaPassword_Happy(t *testing.T) {
	srv := newTestServer(t)
	home := withTempHome(t)
	_ = home
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatal(err)
	}

	in := installerFromTestServer(srv)
	progress := captureProgress{}
	err = in.Install(
		context.Background(),
		Target{User: "alice", Host: srv.Host(), Port: srv.Port()},
		srv.password,
		lk,
		progress.report,
	)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	// The server should now trust our key. The middle base64 chunk
	// is what the dedup grep would look for; comparing on that is
	// stable across comment-field differences.
	want := middleField(lk.PublicLine)
	got := srv.authorizedKeyMaterial()
	if !contains(got, want) {
		t.Errorf("server's authorized keys = %v, want it to contain %q", got, want)
	}

	// Progress must have fired through each stage in order — the
	// wizard relies on this to advance its status line.
	if got := progress.stages(); !equalSlices(got, []string{"hostkey", "connect", "install", "validate", "done"}) {
		t.Errorf("progress stages = %v, want hostkey→connect→install→validate→done", got)
	}

	// Exactly two server-side authenticated sessions: the install
	// (password auth → sh -s) and the validation (publickey →
	// exit). A regression that double-runs install or skips
	// validation would trip this.
	sessions := srv.Sessions()
	if len(sessions) != 2 {
		t.Fatalf("server saw %d sessions, want 2 (install + validate)", len(sessions))
	}
	if sessions[0].method != "password" || sessions[0].cmd != "sh -s" {
		t.Errorf("session 0 = %s/%s, want password/sh -s", sessions[0].method, sessions[0].cmd)
	}
	if sessions[1].method != "publickey" || sessions[1].cmd != "exit" {
		t.Errorf("session 1 = %s/%s, want publickey/exit", sessions[1].method, sessions[1].cmd)
	}
}

// TestInstallKeyViaPassword_WrongPassword — the user fat-fingers the
// password. We must return ErrWrongPassword (not a generic error) so
// the wizard can stay on the password step and re-prompt instead of
// treating it as a hard failure.
func TestInstallKeyViaPassword_WrongPassword(t *testing.T) {
	srv := newTestServer(t)
	withTempHome(t)
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatal(err)
	}
	in := installerFromTestServer(srv)
	err = in.Install(
		context.Background(),
		Target{User: "alice", Host: srv.Host(), Port: srv.Port()},
		"definitely-not-the-password",
		lk,
		nil,
	)
	if err == nil {
		t.Fatal("Install with wrong password should fail")
	}
	if !errors.Is(err, ErrWrongPassword) {
		t.Errorf("error = %v, want errors.Is(err, ErrWrongPassword)", err)
	}
}

// TestInstallKeyViaPassword_Idempotent — running the wizard twice
// against the same remote must not duplicate the key in
// authorized_keys. The installer's grep guards this; a regression
// where the test server sees TWO printf lines would catch a slip.
func TestInstallKeyViaPassword_Idempotent(t *testing.T) {
	srv := newTestServer(t)
	withTempHome(t)
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatal(err)
	}
	// Pre-authorize the key so the second run's validation passes
	// from the start. The test for "install actually writes" is
	// the Happy case; here we care about the dedup contract.
	srv.AuthorizeKey(lk.PublicLine)

	in := installerFromTestServer(srv)
	tgt := Target{User: "alice", Host: srv.Host(), Port: srv.Port()}
	for i := 0; i < 2; i++ {
		if err := in.Install(context.Background(), tgt, srv.password, lk, nil); err != nil {
			t.Fatalf("Install pass %d: %v", i, err)
		}
	}
	// The server records every script body the installer sent.
	// We can't directly count "duplicate appends" because our test
	// shell simulator dedupes by content too (mirroring sshd's
	// behavior would be even more code). But we CAN assert the
	// script itself contains the dedup-grep — if a careless edit
	// dropped the grep, this fires.
	sessions := srv.Sessions()
	for _, s := range sessions {
		if s.cmd != "sh -s" {
			continue
		}
		if !strings.Contains(s.stdin, "grep -F") {
			t.Errorf("install script missing dedup grep:\n%s", s.stdin)
		}
		if !strings.Contains(s.stdin, "chmod 700") || !strings.Contains(s.stdin, "chmod 600") {
			t.Errorf("install script missing perm fixes:\n%s", s.stdin)
		}
	}
}

// TestInstallKeyViaPassword_ValidatesAfterInstall — even if the
// remote write succeeds, validation must connect with the key. We
// simulate "write succeeded but key wasn't actually trusted" by
// disabling key auth on the server BEFORE the install: the password
// auth runs, the script runs, but the post-write key-auth dial fails.
//
// This is the bug we'd see if a remote sshd has KeyAuthentication
// disabled or the authorized_keys path is overridden.
func TestInstallKeyViaPassword_ValidatesAfterInstall(t *testing.T) {
	srv := newTestServer(t)
	withTempHome(t)
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatal(err)
	}
	// Sabotage the install path: make the server REFUSE to add the
	// key to its trust map so validation fails. We do this by
	// pre-clearing applyAuthorizedAppend's effect after the
	// install. The simplest mechanic is to monkey-key the server's
	// trust map to ignore the append.
	srv.disableTrustUpdate = true

	in := installerFromTestServer(srv)
	err = in.Install(
		context.Background(),
		Target{User: "alice", Host: srv.Host(), Port: srv.Port()},
		srv.password,
		lk,
		nil,
	)
	if err == nil {
		t.Fatal("Install must fail when validation can't auth with the key")
	}
	if !strings.Contains(err.Error(), "validation") {
		t.Errorf("error = %v, want 'validation' substring (so users see it surfaced)", err)
	}
}

// TestInstall_ContextCancel — caller cancels mid-flight (user hits
// Esc). Install must abort, not hang on the dial.
func TestInstall_ContextCancel(t *testing.T) {
	srv := newTestServer(t)
	withTempHome(t)
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done

	in := installerFromTestServer(srv)
	err = in.Install(ctx, Target{User: "alice", Host: srv.Host(), Port: srv.Port()}, srv.password, lk, nil)
	if err == nil {
		t.Fatal("Install with cancelled context should error")
	}
}

// installerFromTestServer wires the production installer to dial
// the test server instead of the network. The host-key callback is
// still TOFU — known_hosts lives in withTempHome's HOME isolation
// so the test never touches the user's real ~/.ssh/known_hosts.
func installerFromTestServer(srv *testServer) *installer {
	return &installer{
		dial: func(_ context.Context, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
			// Force the dial onto the test server's listener regardless
			// of what `addr` was — keeps the installer's host string
			// the human-facing one ("alice@server.example") while the
			// transport goes to 127.0.0.1:<random>.
			cfg.Timeout = 3 * time.Second
			return srv.dialFromTest(cfg)
		},
	}
}

// captureProgress collects Progress callbacks for assertion. Used to
// verify the wizard sees each stage transition exactly once.
type captureProgress struct {
	rows []progressRow
}

type progressRow struct {
	stage, detail string
}

func (cp *captureProgress) report(stage, detail string) {
	cp.rows = append(cp.rows, progressRow{stage, detail})
}

func (cp *captureProgress) stages() []string {
	out := make([]string, 0, len(cp.rows))
	for _, r := range cp.rows {
		out = append(out, r.stage)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
