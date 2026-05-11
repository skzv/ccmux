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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
}

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
		if out, err := run(ctx, 2*time.Second, s.BinaryPath, "version"); err == nil {
			s.Version = strings.TrimSpace(out)
		}
		if out, err := run(ctx, 2*time.Second, s.BinaryPath, "status"); err == nil {
			s.StatusRaw = out
			lower := strings.ToLower(out)
			// `moshi-hook status` is human-formatted, not JSON. It reports
			// pairing state only — not live WebSocket connectivity — so
			// we use ServiceRunning (from brew services) as the closest
			// proxy for "the daemon is up and reachable from Moshi".
			s.Paired = strings.Contains(lower, "paired") && !strings.Contains(lower, "not paired") && !strings.Contains(lower, "unpaired")
		}
	}

	s.HooksInstalled = claudeSettingsMentionsMoshi()

	if _, err := exec.LookPath("brew"); err == nil {
		if out, err := run(ctx, 2*time.Second, "brew", "services", "list", "--json"); err == nil {
			s.ServiceRunning = brewServiceStartedFromJSON(out)
		}
	}

	return s
}

// SuppressBell returns true if ccmuxd should skip injecting a BEL into
// session panes on needs-input transitions, because moshi-hook is handling
// notifications. We don't want both firing.
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

// Pair runs `moshi-hook pair --token <token>`.
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
