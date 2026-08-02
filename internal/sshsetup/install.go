package sshsetup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// installer is the production InstallKeyViaPassword path. It's a
// struct (not just a function) so tests can inject a fake dialer
// without monkey-patching package-level state.
type installer struct {
	dial func(ctx context.Context, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error)
}

// defaultInstaller is the production installer. Tests use a fake
// dial that connects to an in-process ssh.Server (see install_test.go).
func defaultInstaller() *installer {
	return &installer{dial: defaultSSHDial}
}

// InstallKeyViaPassword is the headline entry point. It:
//
//  1. Resolves a TOFU host-key callback that records the remote's
//     host key in ~/.ssh/known_hosts on first contact, and fails
//     loudly on a subsequent mismatch.
//  2. Connects with PASSWORD auth using the password the user just
//     typed into the wizard.
//  3. Appends key.PublicLine to remote ~/.ssh/authorized_keys,
//     creating ~/.ssh first if needed, and dedupes by content (so
//     re-running the wizard is a safe no-op).
//  4. Fixes remote perms (chmod 700 ~/.ssh, chmod 600 ~/.ssh/authorized_keys) —
//     sshd refuses to honor authorized_keys when perms are loose.
//  5. Closes the password session and re-connects with KEY auth as a
//     self-test. If that fails, the install is reported as broken
//     even though the file write succeeded — better to surface the
//     failure than to leave the user thinking they're done.
//
// `password` is used only for the bootstrap connection and is not
// retained, logged, or returned.
func InstallKeyViaPassword(ctx context.Context, t Target, password string, key LocalKey, p Progress) error {
	return defaultInstaller().Install(ctx, t, password, key, p)
}

// Install is the testable form of InstallKeyViaPassword — same
// semantics, but the dial seam is settable on the receiver.
func (in *installer) Install(ctx context.Context, t Target, password string, key LocalKey, p Progress) error {
	if t.Host == "" {
		return errors.New("sshsetup: host is required")
	}
	if key.PublicLine == "" {
		return errors.New("sshsetup: public key line is empty")
	}
	user := t.User
	if user == "" {
		return errors.New("sshsetup: user is required")
	}
	// Honor a context that's already cancelled — the wizard's Esc
	// path cancels the parent context and we should bail before
	// touching the network. Without this the in-process dial may
	// complete fast enough that the cancellation goes unnoticed.
	if err := ctx.Err(); err != nil {
		return err
	}

	p.report("hostkey", "resolving known_hosts callback")
	hkCB, err := tofuHostKeyCallback()
	if err != nil {
		return fmt.Errorf("known_hosts: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	p.report("connect", fmt.Sprintf("dialing %s with password auth", t.Addr()))
	pwClient, err := in.dial(cctx, t.Addr(), &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: hkCB,
		Timeout:         10 * time.Second,
	})
	if err != nil {
		return classifyConnectErr(err)
	}
	defer pwClient.Close()

	p.report("install", "appending public key to ~/.ssh/authorized_keys")
	if err := remoteInstallKey(cctx, pwClient, key.PublicLine); err != nil {
		return fmt.Errorf("install key: %w", err)
	}

	// Validate by re-connecting with key auth. Using a fresh client
	// (rather than the existing password session) is the whole point
	// — it proves the remote actually accepted the key, which is
	// exactly the failure mode the wizard is supposed to surface.
	p.report("validate", "reconnecting with key auth to confirm")
	signer, err := loadSigner(key.PrivatePath)
	if err != nil {
		return fmt.Errorf("load private key %s: %w", key.PrivatePath, err)
	}
	// Rebuild the host-key callback for the validation hop. knownhosts.New
	// reads known_hosts ONCE into an in-memory db at construction, so the
	// callback from the password hop (hkCB) still believes this host is
	// unknown — even though the password hop's TOFU just appended its key
	// to disk. Reusing hkCB here makes the validation hop re-run TOFU
	// (accept-and-append) instead of VERIFYING against the recorded key,
	// which defeats the whole point of the second connection: a MITM
	// presenting key A on the password hop and key B on the validation
	// hop would be accepted on both. A fresh callback reads the now-
	// updated file and flags a mismatch.
	hkCBValidate, err := tofuHostKeyCallback()
	if err != nil {
		return fmt.Errorf("known_hosts (validate): %w", err)
	}
	keyClient, err := in.dial(cctx, t.Addr(), &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hkCBValidate,
		Timeout:         10 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("validation: key-auth failed: %w", err)
	}
	defer keyClient.Close()
	// Open one trivial session to confirm the channel handshake
	// also works post-auth. Without this we'd accept a server that
	// auths but refuses every session — a misconfiguration we'd
	// otherwise discover only at attach time.
	sess, err := keyClient.NewSession()
	if err != nil {
		return fmt.Errorf("validation: NewSession: %w", err)
	}
	if err := sess.Run("exit"); err != nil {
		_ = sess.Close()
		return fmt.Errorf("validation: exit: %w", err)
	}
	_ = sess.Close()

	p.report("done", "key installed and validated")
	return nil
}

// remoteInstallKey runs the smallest possible shell snippet on the
// remote to append our public key. Idempotent: if a line matching
// our public-key MATERIAL (the middle base64 chunk) is already
// present, we don't append a duplicate. We match on the middle
// chunk, not the whole line, so re-running the wizard from a
// different machine (where the comment field differs) still dedupes
// correctly.
//
// We deliberately do NOT use sftp here — sftp isn't enabled on every
// sshd install (corp images, BSD jails, etc.), and shell commands
// work everywhere openssh runs.
func remoteInstallKey(ctx context.Context, client *ssh.Client, pubLine string) error {
	mid := middleField(pubLine)
	if mid == "" {
		return errors.New("public key has no base64 middle field")
	}
	// Single-quoted heredoc so the shell never expands $ in the key.
	// `printf '%s\n'` is the most portable way to append exactly one
	// trailing newline; `echo` differs across /bin/sh implementations.
	script := fmt.Sprintf(`set -e
mkdir -p "$HOME/.ssh"
chmod 700 "$HOME/.ssh"
touch "$HOME/.ssh/authorized_keys"
chmod 600 "$HOME/.ssh/authorized_keys"
if ! grep -F %s "$HOME/.ssh/authorized_keys" >/dev/null 2>&1; then
  printf '%%s\n' %s >> "$HOME/.ssh/authorized_keys"
fi
`, shellQuote(mid), shellQuote(pubLine))
	return runRemoteScript(ctx, client, script)
}

// enumerator mirrors installer: production code uses defaultSSHDial;
// tests substitute their own to point at an in-process server.
type enumerator struct {
	dial func(ctx context.Context, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error)
}

func defaultEnumerator() *enumerator { return &enumerator{dial: defaultSSHDial} }

// EnumerateUsers lists the other Unix accounts on the remote with a
// UID at or above the platform's "regular user" threshold (500 on
// macOS, 1000 on Linux). The current user is filtered out. Runs on
// the existing key-auth path — we never re-prompt for a password.
func EnumerateUsers(ctx context.Context, t Target, key LocalKey) ([]string, error) {
	return defaultEnumerator().Enumerate(ctx, t, key)
}

// Enumerate is the testable shape.
func (en *enumerator) Enumerate(ctx context.Context, t Target, key LocalKey) ([]string, error) {
	user := t.User
	if user == "" {
		return nil, errors.New("sshsetup: user is required")
	}
	signer, err := loadSigner(key.PrivatePath)
	if err != nil {
		return nil, err
	}
	hkCB, err := tofuHostKeyCallback()
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := en.dial(cctx, t.Addr(), &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hkCB,
		Timeout:         8 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	defer client.Close()

	// Detect OS so we pick the right enumeration command. `uname`
	// returns Darwin / Linux for the two platforms we care about; we
	// fall back to /etc/passwd on anything else.
	uname, _ := runRemoteCapture(cctx, client, "uname")
	var raw string
	switch strings.TrimSpace(strings.ToLower(uname)) {
	case "darwin":
		// dscl is the canonical macOS user store. UniqueID >= 500
		// excludes root and the _service accounts.
		out, err := runRemoteCapture(cctx, client, "dscl . -list /Users UniqueID")
		if err != nil {
			return nil, err
		}
		raw = parseDscl(out)
	default:
		// /etc/passwd is universal on Linux/BSD/etc. UID >= 1000 is
		// the standard LSB threshold for regular users.
		out, err := runRemoteCapture(cctx, client, "getent passwd 2>/dev/null || cat /etc/passwd")
		if err != nil {
			return nil, err
		}
		raw = parseEtcPasswd(out)
	}
	users := strings.Fields(raw)
	users = filterOut(users, user)
	return uniqStrings(users), nil
}

// parseDscl reads `dscl . -list /Users UniqueID` output and emits
// space-separated usernames whose UID is >= 500. Output rows look
// like:
//
//	root             0
//	_amavisd       284
//	skz            501
//
// We deliberately don't filter underscore-prefixed names by name —
// `_amavisd` has UID 284 (system), but a homegrown account named
// `_alex` with UID 502 would be a real user we want to surface.
func parseDscl(s string) string {
	var keep []string
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		name := f[0]
		uid := atoiSafe(f[1])
		if uid < 500 {
			continue
		}
		keep = append(keep, name)
	}
	return strings.Join(keep, " ")
}

// parseEtcPasswd parses /etc/passwd (or `getent passwd` output) and
// emits usernames whose UID is >= 1000 and whose shell is not
// /usr/sbin/nologin / /sbin/nologin / /bin/false. The shell check
// catches the rare "real user but disabled" entry.
func parseEtcPasswd(s string) string {
	var keep []string
	for _, line := range strings.Split(s, "\n") {
		f := strings.Split(line, ":")
		if len(f) < 7 {
			continue
		}
		name := f[0]
		uid := atoiSafe(f[2])
		shell := f[6]
		if uid < 1000 {
			continue
		}
		if shell == "/usr/sbin/nologin" || shell == "/sbin/nologin" || shell == "/bin/false" {
			continue
		}
		keep = append(keep, name)
	}
	return strings.Join(keep, " ")
}

// classifyConnectErr maps an ssh.Dial error into the most actionable
// shape possible. We catch the well-known "unable to authenticate"
// string from x/crypto/ssh and wrap it with a sentinel so callers
// (CLI + TUI) can show "wrong password?" instead of the raw error.
//
// Host-key mismatch is a special case: tofuHostKeyCallback already
// returns ErrHostKeyMismatch unwrapped, and ssh.Dial threads it
// through as the connection error. We DON'T re-wrap here because
// that produced the double-printed "sshsetup: host key mismatch:
// ssh: handshake failed: sshsetup: host key mismatch" string that
// users saw in the wild — preserve the single sentinel so
// errors.Is still works AND the rendered text reads once.
func classifyConnectErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrHostKeyMismatch) {
		return ErrHostKeyMismatch
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "unable to authenticate"),
		strings.Contains(msg, "auth methods exhausted"),
		strings.Contains(msg, "permission denied"):
		return fmt.Errorf("%w: %v", ErrWrongPassword, err)
	case strings.Contains(msg, "knownhosts"),
		strings.Contains(msg, "key mismatch"),
		strings.Contains(msg, "host key mismatch"):
		return ErrHostKeyMismatch
	}
	return err
}

// ErrWrongPassword is returned from InstallKeyViaPassword when the
// password the user typed didn't authenticate. Distinct sentinel so
// the wizard can stay on the password step and ask again, rather
// than treating it as a generic failure.
var ErrWrongPassword = errors.New("sshsetup: wrong password or password auth disabled")

// ErrHostKeyMismatch is returned when known_hosts has a different
// key for this host than the one the remote presented. Never
// auto-remediate — surface it as a security issue.
var ErrHostKeyMismatch = errors.New("sshsetup: host key mismatch")

// defaultSSHDial is the production SSH dial. Separate function so
// tests can substitute a same-shape dial that points at an in-process
// server without touching the network. Uses dialFilteredTCP so
// non-routable IPv6 link-local addresses (`fe80::...`) — which
// macOS's mDNS resolver sometimes hands back for a Tailscale-named
// peer — never get attempted. A beta tester hit "no route to host"
// dialing exactly that case before this filter existed.
func defaultSSHDial(ctx context.Context, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	conn, err := dialFilteredTCP(ctx, addr, cfg.Timeout)
	if err != nil {
		return nil, err
	}
	cconn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return ssh.NewClient(cconn, chans, reqs), nil
}

// dialFilteredTCP resolves addr, drops IPv6 link-local (`fe80::/10`)
// candidates, and dials the remaining addresses in order. Keeps the
// HOSTNAME (not the IP) as the dial argument to ssh.NewClientConn
// later so known_hosts lookups match what the user thinks of.
//
// We also drop IPv4 link-local (`169.254.0.0/16`) because that's
// what macOS auto-assigns when DHCP fails — also non-routable for
// the SSH use case here.
func dialFilteredTCP(ctx context.Context, addr string, timeout time.Duration) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var candidates []net.IPAddr
	for _, ip := range ips {
		if ip.IP.IsLinkLocalUnicast() {
			continue
		}
		candidates = append(candidates, ip)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no routable address for %s (resolved only link-local: %v)", host, ips)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	d := net.Dialer{Timeout: timeout}
	var lastErr error
	for _, ip := range candidates {
		target := net.JoinHostPort(ip.IP.String(), port)
		conn, err := d.DialContext(ctx, "tcp", target)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("dial %s: no routable candidate succeeded", host)
	}
	return nil, lastErr
}

// tofuHostKeyCallback returns a HostKeyCallback that:
//   - reads ~/.ssh/known_hosts on every call,
//   - on a missing entry: appends the new host key and ACCEPTS the
//     connection (TOFU — same behavior as the openssh client with
//     StrictHostKeyChecking=accept-new),
//   - on a mismatched entry: returns ErrHostKeyMismatch unwrapped, so
//     ssh.NewClientConn surfaces it as a real failure.
func tofuHostKeyCallback() (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return nil, err
	}
	khPath := filepath.Join(sshDir, "known_hosts")
	if !fileExists(khPath) {
		// Create an empty known_hosts so knownhosts.New doesn't
		// fail on a fresh ~/.ssh. The first call appends to it.
		if err := os.WriteFile(khPath, nil, 0o644); err != nil {
			return nil, err
		}
	}
	knownCB, err := knownhosts.New(khPath)
	if err != nil {
		return nil, err
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := knownCB(hostname, remote, key)
		if err == nil {
			return nil
		}
		// knownhosts returns a *KeyError; .Want == nil means "no
		// entry for this host" (first contact, TOFU-add it) while
		// non-empty .Want means a recorded entry exists but
		// differs (a real mismatch — refuse).
		var keErr *knownhosts.KeyError
		if errors.As(err, &keErr) && len(keErr.Want) == 0 {
			return appendKnownHost(khPath, hostname, key)
		}
		return ErrHostKeyMismatch
	}, nil
}

// appendKnownHost writes a single `<hostname> <key-type> <base64>`
// line to known_hosts, but only if a matching line isn't already
// there. Without the dedup, every call to the host-key callback
// appends — and Install opens TWO connections (password + key
// validate), so a clean wizard run would otherwise leave two
// identical lines. Bigger problem: re-running the wizard would
// accumulate one duplicate per run forever.
func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	line := knownhosts.Line([]string{hostname}, key)
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Contains(existing, []byte(line)) {
			return nil
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		return err
	}
	return nil
}

// loadSigner reads a private key file (with no passphrase) and turns
// it into an ssh.Signer the client can present. Passphrase-protected
// keys are out of scope here — the wizard's whole point is a
// zero-prompt attach flow.
func loadSigner(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		// Re-wrap so callers can surface a passphrase hint instead
		// of the generic "ssh: this private key is passphrase
		// protected" string.
		if strings.Contains(err.Error(), "passphrase protected") {
			return nil, fmt.Errorf("private key %s is passphrase-protected; ccmux's setup wizard needs a passphrase-less key", path)
		}
		return nil, err
	}
	return signer, nil
}

// runRemoteScript runs a `bash -c "<script>"` body on the remote and
// returns an error if the exit status is non-zero. Stderr is folded
// into the error message so the wizard can show what blew up.
func runRemoteScript(ctx context.Context, client *ssh.Client, script string) error {
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	var stderr bytes.Buffer
	sess.Stderr = &stderr
	// We feed the script over stdin and run `sh -s` to avoid argv
	// length limits and shell-quoting nightmares for long keys.
	sess.Stdin = strings.NewReader(script)
	if err := sess.Run("sh -s"); err != nil {
		s := strings.TrimSpace(stderr.String())
		if s != "" {
			return fmt.Errorf("%v: %s", err, s)
		}
		return err
	}
	_ = ctx // session is bound by the parent SSH connection's deadlines
	return nil
}

// runRemoteCapture runs `cmd` on the remote and returns combined stdout.
// stderr is folded back into the error if exit status is non-zero.
func runRemoteCapture(ctx context.Context, client *ssh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr
	if err := sess.Run(cmd); err != nil {
		s := strings.TrimSpace(stderr.String())
		if s != "" {
			return stdout.String(), fmt.Errorf("%v: %s", err, s)
		}
		return stdout.String(), err
	}
	_ = ctx
	return stdout.String(), nil
}

// middleField returns the second whitespace-separated field of an
// authorized-keys line (the base64 chunk). Used as the grep needle
// for idempotency: comment fields ("user@host") may differ across
// machines, but the key material is unique per key.
func middleField(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

// shellQuote wraps s in single quotes, escaping any embedded single
// quotes for POSIX sh. Used so the remote script can take our key
// (which may contain spaces, comment fields, and other shell-active
// characters) without re-interpretation.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// atoiSafe is strconv.Atoi but returns -1 on parse failure, so
// `uid < threshold` checks can ignore malformed lines cleanly.
func atoiSafe(s string) int {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func filterOut(xs []string, drop string) []string {
	out := xs[:0]
	for _, s := range xs {
		if s == drop {
			continue
		}
		out = append(out, s)
	}
	return out
}

func uniqStrings(xs []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(xs))
	for _, s := range xs {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// discard is a sink we hand to ssh.Session.Stdout when we don't care
// about the body — keeps the goroutine pump quiet when the only
// signal we want is the exit status.
var _ = io.Discard
