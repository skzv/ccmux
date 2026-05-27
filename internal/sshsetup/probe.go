package sshsetup

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ProbeResult is the structured outcome of a non-interactive SSH
// reachability check. Callers map it to a specific UI message and a
// follow-up action; we deliberately never return a raw stderr string
// because sshd's error messages vary across distros and confuse end
// users (the "could not resolve hostname" vs "Connection refused" vs
// "Permission denied (publickey)" zoo).
type ProbeResult int

const (
	// ProbeUnknown is the zero value, used only on package-internal
	// errors that don't fit any specific bucket. Callers should treat
	// it as a generic failure.
	ProbeUnknown ProbeResult = iota

	// ProbeOK means key-based auth succeeded. The caller can proceed
	// straight to attach with no setup needed.
	ProbeOK

	// ProbeAuthFailed means the host accepted the connection but
	// rejected key auth — the classic "Permission denied (publickey)"
	// state. This is the path that triggers the wizard.
	ProbeAuthFailed

	// ProbeSshdDisabled means TCP refused the connection on port 22.
	// On macOS this almost always means "Remote Login is off in
	// System Settings → Sharing". The UI surfaces that specific
	// remediation rather than a generic refused-connection message.
	ProbeSshdDisabled

	// ProbeRefused is the same shape as ProbeSshdDisabled but on a
	// non-22 port, where the right remediation is different (a
	// firewall, a wrong port, or sshd bound to a different address).
	ProbeRefused

	// ProbeTimeout means TCP never came up at all — usually a
	// disconnected Tailscale peer or a routing problem.
	ProbeTimeout

	// ProbeHostKeyMismatch means we have a known_hosts entry for this
	// host but the host key changed. Never auto-remediate this — it
	// can mask a MITM. The UI must require manual confirmation.
	ProbeHostKeyMismatch

	// ProbeNoNetwork is a special case: DNS resolution failed (no
	// such host, NXDOMAIN, or Tailscale isn't routing the magicdns
	// name).
	ProbeNoNetwork
)

// String renders the result as a stable short token, used for tests
// and structured logging. The human-facing message lives in the UI
// layer, not here.
func (r ProbeResult) String() string {
	switch r {
	case ProbeOK:
		return "ok"
	case ProbeAuthFailed:
		return "auth-failed"
	case ProbeSshdDisabled:
		return "sshd-disabled"
	case ProbeRefused:
		return "refused"
	case ProbeTimeout:
		return "timeout"
	case ProbeHostKeyMismatch:
		return "host-key-mismatch"
	case ProbeNoNetwork:
		return "no-network"
	default:
		return "unknown"
	}
}

// IsSetupNeeded returns true when the result indicates the user should
// run the SSH setup wizard. ProbeAuthFailed is the obvious one; other
// states either need manual remediation (HostKeyMismatch, SshdDisabled)
// or aren't fixable from inside ccmux (NoNetwork, Timeout).
func (r ProbeResult) IsSetupNeeded() bool {
	return r == ProbeAuthFailed
}

// probeTimeout is the wall-clock budget for one Probe call. Short
// enough that a wedged peer can't stall a UI flow, long enough for
// a slow VPN handshake. Tuned for the "attach probe before exec'ing
// mosh" path where every millisecond counts.
const probeTimeout = 4 * time.Second

// Prober is the dependency we mock in tests so we don't shell out to
// a real ssh binary or wait on real network timeouts. The default
// implementation, defaultProber, calls the system ssh binary with
// BatchMode=yes so it never blocks on a password prompt.
type Prober interface {
	Probe(ctx context.Context, t Target) ProbeResult
}

// DefaultProber returns the production prober. Pulled out as a
// function (not a var) so tests can substitute their own without a
// package-global mutation race.
func DefaultProber() Prober { return defaultProber{} }

type defaultProber struct{}

// Probe is a convenience wrapper around DefaultProber().Probe so
// callers don't have to instantiate one. Test code should construct
// its own prober and pass it down rather than monkey-patching here.
func Probe(ctx context.Context, t Target) ProbeResult {
	return DefaultProber().Probe(ctx, t)
}

func (defaultProber) Probe(ctx context.Context, t Target) ProbeResult {
	if t.Host == "" {
		return ProbeUnknown
	}

	// First gate: can we even dial TCP? Doing this *before* shelling
	// out to ssh lets us distinguish "sshd is off" from "key auth
	// failed" cleanly. The ssh binary collapses both into the same
	// error stream in some environments.
	tcp := probeTCP(ctx, t)
	if tcp != ProbeOK {
		return tcp
	}

	// TCP is up, so any further failure is auth-related or host-key.
	// Shell out to ssh with BatchMode=yes (refuse all prompts) and
	// a short ConnectTimeout. We don't need to actually run a remote
	// command — `exit` is the minimum body.
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	user := t.User
	host := t.Host
	target := host
	if user != "" {
		target = user + "@" + host
	}
	port := t.Port
	if port == 0 {
		port = 22
	}

	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=3",
		"-o", "StrictHostKeyChecking=accept-new",
		// IdentitiesOnly=yes makes ssh ignore ssh-agent identities
		// and use only the explicit IdentityFile entries (default
		// ~/.ssh/id_ed25519, etc.). Without it, an agent with
		// MaxAuthTries-worth of irrelevant keys can exhaust auth
		// before the user's actual key is offered — which surfaces
		// as ProbeAuthFailed even though the right key is on disk.
		"-o", "IdentitiesOnly=yes",
		"-p", fmt.Sprintf("%d", port),
		target, "exit",
	}
	cmd := exec.CommandContext(cctx, "ssh", args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return ProbeOK
	}
	r := classifyProbeStderr(string(out), port)
	if r != ProbeUnknown {
		return r
	}
	// Catch context-cancellation that didn't get a chance to emit
	// any stderr — manifests as exit 255 with no message.
	if cctx.Err() != nil {
		return ProbeTimeout
	}
	return ProbeUnknown
}

// classifyProbeStderr is the pure-function half of the probe. Lifted
// out of Probe() so unit tests can hammer every branch without
// needing a real ssh binary on PATH. Substring matching is
// deliberately conservative — the alternative (parsing openssh's
// numeric exit code) is even less stable across versions.
func classifyProbeStderr(stderr string, port int) ProbeResult {
	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "host key verification failed"),
		strings.Contains(s, "remote host identification has changed"):
		return ProbeHostKeyMismatch
	case strings.Contains(s, "permission denied"),
		strings.Contains(s, "no supported authentication methods"):
		return ProbeAuthFailed
	case strings.Contains(s, "connection timed out"),
		strings.Contains(s, "operation timed out"):
		return ProbeTimeout
	case strings.Contains(s, "could not resolve hostname"),
		strings.Contains(s, "name or service not known"):
		return ProbeNoNetwork
	case strings.Contains(s, "connection refused"):
		if port == 22 || port == 0 {
			return ProbeSshdDisabled
		}
		return ProbeRefused
	}
	return ProbeUnknown
}

// probeTCP does a tight dial to host:port to differentiate "sshd
// off" / "wrong port" / "no network" from auth failures *before* we
// pay the cost of running the ssh binary. Returns ProbeOK iff the
// TCP handshake completes; any other ProbeResult means the caller
// should short-circuit and return that value.
func probeTCP(ctx context.Context, t Target) ProbeResult {
	port := t.Port
	if port == 0 {
		port = 22
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	cctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	conn, err := d.DialContext(cctx, "tcp", fmt.Sprintf("%s:%d", t.Host, port))
	if err == nil {
		_ = conn.Close()
		return ProbeOK
	}
	// Classify the dial error.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ProbeNoNetwork
	}
	if isConnRefused(err) {
		if port == 22 {
			return ProbeSshdDisabled
		}
		return ProbeRefused
	}
	if isTimeout(err) || errors.Is(err, context.DeadlineExceeded) {
		return ProbeTimeout
	}
	// Unknown dial failure — fall through to "no network" so the UI
	// suggests a Tailscale check rather than a confusing generic.
	return ProbeNoNetwork
}

// isConnRefused checks for ECONNREFUSED across the layered error
// chain net.Dial wraps. syscall.ECONNREFUSED matches direct dial
// failures; the substring fallback catches platforms where the
// chain is normalized differently.
func isConnRefused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "connection refused")
}

// isTimeout reports whether the error came from a connect-side
// deadline. We treat any net.Error with Timeout() == true plus the
// generic deadline-exceeded sentinel as a timeout.
func isTimeout(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}
