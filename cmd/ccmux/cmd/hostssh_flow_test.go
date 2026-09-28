package cmd

import (
	"bufio"
	"context"
	"os"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/sshsetup"
)

// stubSSHSetup swaps every network and terminal seam runHostSetupSSH
// uses for fakes: the probe reports that only key auth is missing, the
// local key is a placeholder, the password prompt answers at once, and
// install / enumerate succeed unless their context is already done.
// Tests override individual seams after calling it.
func stubSSHSetup(t *testing.T) {
	t.Helper()
	origProbe, origKey, origInstall, origEnum, origPW, origTimeout, origReader :=
		sshProbe, sshEnsureLocalKey, sshInstallKey, sshEnumerateUsers, sshReadPassword, sshStepTimeout, promptReader
	t.Cleanup(func() {
		sshProbe, sshEnsureLocalKey, sshInstallKey, sshEnumerateUsers, sshReadPassword, sshStepTimeout, promptReader =
			origProbe, origKey, origInstall, origEnum, origPW, origTimeout, origReader
	})
	sshProbe = func(context.Context, sshsetup.Target) sshsetup.ProbeResult { return sshsetup.ProbeAuthFailed }
	sshEnsureLocalKey = func() (sshsetup.LocalKey, error) {
		return sshsetup.LocalKey{PrivatePath: "/nonexistent/id_ed25519"}, nil
	}
	sshReadPassword = func(string) (string, error) { return "hunter2", nil }
	sshInstallKey = func(ctx context.Context, _ sshsetup.Target, _ string, _ sshsetup.LocalKey, _ sshsetup.Progress) error {
		return ctx.Err()
	}
	sshEnumerateUsers = func(ctx context.Context, _ sshsetup.Target, _ sshsetup.LocalKey) ([]string, error) {
		return nil, ctx.Err()
	}
}

// pipeStdin makes os.Stdin a pipe holding input (already closed for
// writing) and points the shared prompt reader at it.
func pipeStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		_ = r.Close()
	})
	reader := bufio.NewReader(r)
	promptReader = func() *bufio.Reader { return reader }
}

// TestRunHostSetupSSH_SlowPasswordDoesNotExpireInstall — the password
// prompt and every network step shared one 60s context, so a user who
// took longer than that to type the password had the key install fail
// with "context deadline exceeded". Each network step must get its own
// deadline, started after the prompt.
func TestRunHostSetupSSH_SlowPasswordDoesNotExpireInstall(t *testing.T) {
	isolateHome(t)
	stubSSHSetup(t)
	sshStepTimeout = 100 * time.Millisecond
	sshReadPassword = func(string) (string, error) {
		time.Sleep(3 * sshStepTimeout) // a slow typist
		return "hunter2", nil
	}

	if err := runHostSetupSSH("alice@sputnik.example", false); err != nil {
		t.Fatalf("setup-ssh after a slow password entry: %v", err)
	}
}

// TestRunHostSetupSSH_SkipsUnusableEnumeratedAccounts — account names
// come from the remote. One starting with '-' was offered and stored as
// a host whose `user@host` ssh target begins with '-', i.e. an ssh
// option; such names are skipped without consuming an answer.
func TestRunHostSetupSSH_SkipsUnusableEnumeratedAccounts(t *testing.T) {
	isolateHome(t)
	stubSSHSetup(t)
	sshEnumerateUsers = func(context.Context, sshsetup.Target, sshsetup.LocalKey) ([]string, error) {
		return []string{"-oProxyCommand=touch /tmp/pwned", "evil;id", "bob"}, nil
	}
	sshInstallKey = func(context.Context, sshsetup.Target, string, sshsetup.LocalKey, sshsetup.Progress) error { return nil }
	pipeStdin(t, "y\ny\ny\n")

	if err := runHostSetupSSH("alice@sputnik.example", false); err != nil {
		t.Fatalf("setup-ssh: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts) != 1 || cfg.Hosts[0].User != "bob" {
		t.Errorf("hosts added = %+v, want only bob", cfg.Hosts)
	}
}

// TestRunHostSetupSSH_PipedAnswersAllReachTheirPrompts — the password
// read and every "Add …?" confirm built a fresh bufio.Reader over
// stdin; the first one read ahead and swallowed the rest, so with piped
// input every answer after the first was lost (read as "no").
func TestRunHostSetupSSH_PipedAnswersAllReachTheirPrompts(t *testing.T) {
	isolateHome(t)
	stubSSHSetup(t)
	sshReadPassword = readPassword // the real prompt: stdin is a pipe, so it reads a line
	var gotPassword string
	sshInstallKey = func(ctx context.Context, _ sshsetup.Target, pw string, _ sshsetup.LocalKey, _ sshsetup.Progress) error {
		gotPassword = pw
		return ctx.Err()
	}
	sshEnumerateUsers = func(context.Context, sshsetup.Target, sshsetup.LocalKey) ([]string, error) {
		return []string{"bob", "carol", "dave"}, nil
	}
	pipeStdin(t, "hunter2\ny\nn\nyes\n")

	if err := runHostSetupSSH("alice@sputnik.example", false); err != nil {
		t.Fatalf("setup-ssh: %v", err)
	}
	if gotPassword != "hunter2" {
		t.Errorf("password = %q, want hunter2", gotPassword)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, h := range cfg.Hosts {
		names = append(names, h.Name)
	}
	if len(names) != 2 || names[0] != "bob@sputnik" || names[1] != "dave@sputnik" {
		t.Errorf("hosts added = %v, want [bob@sputnik dave@sputnik] (answers y, n, yes)", names)
	}
}
