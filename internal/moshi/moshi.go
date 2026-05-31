// Package moshi integrates ccmux with Moshi (getmoshi.app), an iOS/Android
// terminal app for AI coding agents. Specifically it integrates with
// `moshi-hook`, the daemon Moshi provides that bridges Claude Code's hooks
// system to push notifications on the phone.
//
// Detection is best-effort and side-effect-free: we never invoke
// moshi-hook on import, only when callers explicitly ask. Used both by
// `ccmux doctor` (reports state) and by ccmuxd (suppresses its own
// bell-injection when moshi-hook is present, to avoid duplicate
// notifications).
package moshi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/keychain"
)

// Status is a snapshot of the user's Moshi/moshi-hook configuration on
// this host. Filled in by Detect.
type Status struct {
	// BinaryInstalled is true if `moshi-hook` is on PATH.
	BinaryInstalled bool

	// BinaryPath is the absolute path to moshi-hook if installed.
	BinaryPath string

	// Version is the output of `moshi-hook version` if installed.
	Version string

	// Paired is true if `moshi-hook status` reports successful pairing.
	Paired bool

	// HooksInstalled is true if ~/.claude/settings.json contains the
	// moshi-hook hook entries (i.e. `moshi-hook install` has been run).
	HooksInstalled bool

	// ServiceRunning is true if `brew services` reports moshi-hook as
	// started (best-effort; only checked on macOS).
	ServiceRunning bool

	// StatusRaw is the verbatim output of `moshi-hook status`, useful
	// for surfacing in the TUI/doctor output.
	StatusRaw string

	// StatusErr holds the error from `moshi-hook status` when that
	// command failed to run at all (timeout, crash, non-zero exit).
	// When non-nil, Paired is not meaningful — we could not determine
	// pairing, which is different from "determined: not paired". Carries
	// the command's stderr so `ccmux doctor` can show why.
	StatusErr error

	// ServiceErr holds the error from `brew services list` when it
	// failed. When non-nil, ServiceRunning could not be determined.
	ServiceErr error
}

// Detection timeouts. moshi-hook's own subcommands are local and
// near-instant, but `brew services list` shells out to Ruby and can be
// slow on a cold cache — so the budgets are generous. A timeout here is
// silently read as "not configured", which nagged users who were in
// fact set up; the headroom keeps that from happening in practice.
const (
	moshiHookTimeout = 5 * time.Second
	brewListTimeout  = 8 * time.Second
)

// ErrKeychainLocked marks a moshi-hook pairing result as untrustworthy:
// the login keychain is locked, so moshi-hook can't read its pairing
// secret and reports `unpaired` even on a host that is in fact paired.
var ErrKeychainLocked = errors.New("login keychain is locked — moshi-hook's pairing secret can't be read. " +
	"This is expected when you SSH into a Mac with no console login. " +
	"Fix: run `security unlock-keychain`, or enable auto-login so the keychain unlocks at boot")

// Detect returns the current Moshi/moshi-hook state on this host. Returns
// a partial Status if any check fails; callers should inspect individual
// fields rather than treating one missing piece as a fatal error.
func Detect(ctx context.Context) Status {
	s := Status{}
	if p, err := exec.LookPath("moshi-hook"); err == nil {
		s.BinaryInstalled = true
		s.BinaryPath = p
	}

	if s.BinaryInstalled {
		if out, err := run(ctx, moshiHookTimeout, s.BinaryPath, "version"); err == nil {
			s.Version = strings.TrimSpace(out)
		}
		if out, err := run(ctx, moshiHookTimeout, s.BinaryPath, "status"); err != nil {
			s.StatusErr = withOutput(err, out)
		} else {
			s.StatusRaw = out
			s.Paired = statusReportsPaired(out)
		}
	}

	s.HooksInstalled = claudeSettingsMentionsMoshi()

	if _, err := exec.LookPath("brew"); err == nil {
		if out, err := run(ctx, brewListTimeout, "brew", "services", "list", "--json"); err != nil {
			s.ServiceErr = withOutput(err, out)
		} else {
			s.ServiceRunning = brewServiceStartedFromJSON(out)
		}
	}

	// moshi-hook keeps its pairing secret in the macOS keychain, so a
	// locked keychain makes `moshi-hook status` cleanly report
	// `unpaired` on a host that is in fact paired. Probe the keychain
	// only when a not-paired verdict is still in doubt — this keeps the
	// `security` shell-out off the daemon's hot path for paired hosts.
	if needsKeychainProbe(s) && keychain.Locked(ctx) {
		s.StatusErr = ErrKeychainLocked
	}

	return s
}

// needsKeychainProbe reports whether a not-paired result is still in
// doubt — i.e. worth the cost of probing keychain lock state. False for
// an already-paired host (keeps `security` off the daemon's hot path)
// and for one whose status command already produced its own error.
func needsKeychainProbe(s Status) bool {
	return s.BinaryInstalled && !s.Paired && s.StatusErr == nil
}

// SuppressBell returns true if ccmuxd should skip terminal BEL
// notifications on needs-input transitions, because moshi-hook is
// handling notifications. We don't want both firing.
func (s Status) SuppressBell() bool {
	return s.HooksInstalled || (s.BinaryInstalled && s.Paired)
}

// InstallCmds returns the canonical commands a host operator would run to
// install moshi-hook from scratch via Homebrew. ccmux can execute these
// for the user via `ccmux moshi-setup`, or just print them.
func InstallCmds() [][]string {
	return [][]string{
		{"brew", "tap", "rjyo/moshi"},
		{"brew", "install", "moshi-hook"},
	}
}

// Pair runs `moshi-hook pair --token <token>`. This is the
// headless/scripted path. For interactive setup prefer HostSetup
// (QR-code Easy Pair), which is what the wizard and `ccmux moshi-setup`
// use by default.
func Pair(ctx context.Context, token string) error {
	if token == "" {
		return errors.New("moshi: pairing token required")
	}
	bin, err := exec.LookPath("moshi-hook")
	if err != nil {
		return fmt.Errorf("moshi-hook not on PATH; brew install rjyo/moshi/moshi-hook first")
	}
	_, err = run(ctx, 30*time.Second, bin, "pair", "--token", token)
	return err
}

// HostSetup runs `moshi-hook host setup` with stdio passthrough. This
// is the "Easy Pair" flow: moshi-hook prints a QR code in the terminal
// that the user scans with the Moshi iOS app to complete the SSH/Mosh
// pairing. No token to copy-paste, no Settings → Integrations menu
// dive — open the app, scan, done.
//
// Returns the combined stdout+stderr output (also written live to the
// user's terminal) so callers can scan it for known recoverable
// errors and offer remediation. Empty string + error means we
// couldn't even start the subprocess (binary missing, etc.).
//
// The 5-minute timeout is generous: the user has to find their phone,
// open the app, navigate to scan, point at the screen. Shorter
// timeouts trip people up.
func HostSetup(ctx context.Context) (string, error) {
	bin, err := exec.LookPath("moshi-hook")
	if err != nil {
		return "", fmt.Errorf("moshi-hook not on PATH; brew install rjyo/moshi/moshi-hook first")
	}
	c, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(c, bin, "host", "setup")
	cmd.Stdin = os.Stdin
	var buf strings.Builder
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	err = cmd.Run()
	return buf.String(), err
}

// HostSetupFix is one recoverable error condition recognized in the
// output of `moshi-hook host setup`, plus the suggested fix.
type HostSetupFix struct {
	// Problem is a one-line human description ("Remote Login is disabled").
	Problem string
	// Description is the user-facing label for the fix (shown in a
	// prompt: "enable Remote Login (SSH) on this Mac").
	Description string
	// Command + Args is the exact shell-out to remediate. Typically
	// invokes sudo internally; the caller is responsible for running
	// with stdio passthrough so the password prompt reaches the user.
	Command string
	Args    []string
	// SettingsURL, when non-empty, is an "x-apple.systempreferences:…"
	// link the caller can offer as the GUI alternative to Command.
	SettingsURL string
}

// DetectFix scans `moshi-hook host setup` output for known
// prerequisite failures and returns the suggested remediation. Returns
// (HostSetupFix{}, false) when the failure isn't one we recognize —
// the caller should fall back to a generic "retry or skip" prompt.
//
// Designed to be extended: each known issue is one entry in the list.
// Keep matches narrow so we don't suggest the wrong fix.
func DetectFix(output string) (HostSetupFix, bool) {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "remote login is not enabled"):
		return HostSetupFix{
			Problem:     "Remote Login (SSH) is disabled on this Mac",
			Description: "Enable Remote Login via `sudo moshi-hook host enable-ssh`",
			Command:     "sudo",
			Args:        []string{"moshi-hook", "host", "enable-ssh"},
			SettingsURL: "x-apple.systempreferences:com.apple.Sharing-Settings.extension",
		}, true
	}
	return HostSetupFix{}, false
}

// InstallHooks runs `moshi-hook install`, which writes the hook entries
// into the user's agent config files (e.g. ~/.claude/settings.json).
func InstallHooks(ctx context.Context) error {
	bin, err := exec.LookPath("moshi-hook")
	if err != nil {
		return fmt.Errorf("moshi-hook not on PATH")
	}
	_, err = run(ctx, 30*time.Second, bin, "install")
	return err
}

// StartService runs `brew services start moshi-hook` on macOS. On Linux
// the upstream recommendation is to run `moshi-hook serve` under a
// systemd-user unit; for now ccmux delegates that to the user.
func StartService(ctx context.Context) error {
	if _, err := exec.LookPath("brew"); err != nil {
		return errors.New("brew not on PATH; on Linux start moshi-hook as a systemd-user service instead")
	}
	_, err := run(ctx, 30*time.Second, "brew", "services", "start", "moshi-hook")
	return err
}

// withOutput folds a failed command's combined output into its error so
// the diagnostic isn't lost. moshi-hook and brew print the actual reason
// to stderr; without this a caller only sees a bare "exit status 1". The
// output is trimmed to its first line — enough to diagnose, no spew.
func withOutput(err error, out string) error {
	out = strings.TrimSpace(out)
	if out == "" {
		return err
	}
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = strings.TrimSpace(out[:i])
	}
	return fmt.Errorf("%w: %s", err, out)
}

// run is a tiny helper around exec that adds a context timeout and
// returns stdout+stderr.
func run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(c, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// claudeSettingsMentionsMoshi looks for the moshi-hook entries that
// `moshi-hook install` writes into ~/.claude/settings.json. The presence
// of any "moshi-hook" substring in the file is sufficient — we don't try
// to parse Claude Code's settings schema here.
func claudeSettingsMentionsMoshi() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, name := range []string{"settings.json", "settings.local.json"} {
		p := filepath.Join(home, ".claude", name)
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.Contains(string(b), "moshi-hook") {
			return true
		}
	}
	return false
}

// statusReportsPaired interprets the output of `moshi-hook status`. That
// command is human-formatted (not JSON) and prints a `status:` line that
// reads `paired` once pairing has succeeded. We treat the presence of
// "paired" as affirmative unless it's negated ("not paired" / "unpaired"),
// so a future wording tweak on the negative side doesn't read as paired.
//
// Note this reports pairing state only — not live WebSocket connectivity.
// Callers use ServiceRunning (from brew services) as the closest proxy
// for "the daemon is up and reachable from Moshi".
func statusReportsPaired(out string) bool {
	lower := strings.ToLower(out)
	return strings.Contains(lower, "paired") &&
		!strings.Contains(lower, "not paired") &&
		!strings.Contains(lower, "unpaired")
}

// brewServiceStartedFromJSON parses `brew services list --json` and
// returns true if moshi-hook's status is "started". Newer brew prints
// JSON; older versions print a table — we tolerate either by falling
// back to a substring scan.
func brewServiceStartedFromJSON(out string) bool {
	out = strings.TrimSpace(out)
	if !strings.HasPrefix(out, "[") {
		// Fallback: table format. Look for "moshi-hook" followed by "started".
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "moshi-hook") && strings.Contains(line, "started") {
				return true
			}
		}
		return false
	}
	var rows []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return false
	}
	for _, r := range rows {
		if r.Name == "moshi-hook" && r.Status == "started" {
			return true
		}
	}
	return false
}
