package tui

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/remoteattach"
	"github.com/skzv/ccmux/internal/sshsetup"
)

// sshWizardResume is the opaque payload the SSH setup wizard carries
// from open to completion (openSSHWizardMsg.resume → wizardCompletedMsg.resume).
// It tells the App what the user was actually trying to do, so a
// successful setup can finish the job rather than just toasting.
type sshWizardResume struct {
	// OpenShell, when true, means "drop into an interactive ssh
	// shell on the target after setup succeeds." This is the
	// Network-tab intent: the user picked a device to get into it,
	// and the wizard was only the auth detour. Without this the
	// wizard installed the key and then stranded the user on the
	// Network screen — the reported bug.
	OpenShell bool
}

// shouldOpenShell reports whether a wizard-completion resume payload
// asks for an interactive shell, returning the target to connect to.
// Split out so the decision is unit-testable without driving
// tea.ExecProcess.
func shouldOpenShell(resume any, target sshsetup.Target) (sshsetup.Target, bool) {
	r, ok := resume.(sshWizardResume)
	if !ok || !r.OpenShell {
		return sshsetup.Target{}, false
	}
	return target, true
}

// sshShellCommand builds the `ssh -t [-p port] user@host` argv for
// dropping into an interactive shell on `target`. Pure + exported to
// the package so a test can assert the exact argv (the bit that
// actually matters); the App wraps it in tea.ExecProcess.
func sshShellCommand(target sshsetup.Target) *exec.Cmd {
	dial := target.Host
	if u := strings.TrimSpace(target.User); u != "" {
		dial = u + "@" + target.Host
	}
	return remoteattach.SSHInteractive(dial, target.Port)
}

// sshShellExec wraps sshShellCommand in a tea.ExecProcess so the App
// can hand it back as a command. On return from the shell it emits
// attachExitedMsg (nil err) to clear any overlay + refresh, matching
// the Network screen's own SSHCmd.
func sshShellExec(target sshsetup.Target) tea.Cmd {
	cmd := sshShellCommand(target)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return attachExitedMsg{Err: err}
	})
}

// persistWizardAdded writes new user@host entries to hosts.toml
// after the user confirmed the multi-select on the wizard's
// enumerate step. Persists the existing app config plus the new
// rows. Errors are surfaced as toasts rather than returned because
// the wizard has already done the irreversible part (installed the
// key); a config-save failure is recoverable next time the user
// runs the wizard.
//
// The added rows reuse the target's address + SSH port; the only
// difference is the user. The wizard's port is the remote sshd port,
// so it goes in SSHPort — config.Host.Port is the ccmuxd HTTP port
// and stays 0 (default 7474). Writing 22 there made every added row
// dial ccmuxd on :22. Host names are derived from
// "<user>@<short-name>" via networkHostShortName so they read
// reasonably in `ccmux host list`.
func persistWizardAdded(a App, target sshsetup.Target, addedUsers []string) App {
	if len(addedUsers) == 0 {
		return a
	}
	shortHost := networkHostShortName(target.Host)
	addHosts := func(cfg *config.Config) {
		for _, u := range addedUsers {
			name := fmt.Sprintf("%s@%s", u, shortHost)
			// Skip if the name is already present — re-running the
			// wizard shouldn't create duplicate rows.
			if hostExistsByName(*cfg, name) {
				continue
			}
			cfg.Hosts = append(cfg.Hosts, config.Host{
				Name:    name,
				Address: target.Host,
				User:    u,
				SSHPort: target.Port,
				Mosh:    true,
			})
		}
	}
	saved, err := config.Update(func(c *config.Config) error { addHosts(c); return nil })
	if err != nil {
		// Keep the rest of the flow successful. We deliberately don't
		// fire a toast here; the caller already emits a success toast.
		if dbg := debugLogger(); dbg != nil {
			dbg.Printf("persist wizard-added users: %v", err)
		}
		return a
	}
	a.adoptConfig(saved)
	return a
}

// persistWizardCorrection saves a username / SSH port the user fixed
// on the wizard's Username step back to the configured host the wizard
// ran for. Without it the key got installed for the corrected account
// but hosts.toml kept the old user (or port), so the Network row still
// showed it and the next attach failed the same way. original is the
// target the wizard opened with, final the one it completed with.
//
// The host is matched by address + effective SSH port + the original
// user; a host with no configured user (the wizard then guessed the
// local $USER) matches only when no row names the user explicitly.
// Discovered tailnet peers have no config row, so nothing is written
// for them. Like persistWizardAdded, a save failure is logged, not
// surfaced — the key install already succeeded.
func persistWizardCorrection(a App, original, final sshsetup.Target) App {
	userChanged := final.User != "" && final.User != original.User
	finalPort := sshPortOr22(final.Port)
	if original.Host == "" || (!userChanged && finalPort == sshPortOr22(original.Port)) {
		return a
	}
	saved, err := config.Update(func(c *config.Config) error {
		changed := false
		for _, i := range wizardHostMatches(c.Hosts, original) {
			h := &c.Hosts[i]
			if userChanged && h.User != final.User {
				h.User = final.User
				changed = true
			}
			if h.EffectiveSSHPort() != finalPort {
				h.SSHPort = finalPort
				if finalPort == 22 {
					h.SSHPort = 0 // the default; keep hosts.toml tidy
				}
				changed = true
			}
		}
		if !changed {
			return errNoWizardCorrection // abort: nothing to write
		}
		return nil
	})
	if err != nil {
		if !errors.Is(err, errNoWizardCorrection) {
			if dbg := debugLogger(); dbg != nil {
				dbg.Printf("persist wizard user/port correction: %v", err)
			}
		}
		return a
	}
	a.adoptConfig(saved)
	return a
}

// errNoWizardCorrection aborts persistWizardCorrection's config.Update
// without a write when no configured host needed changing (e.g. the
// wizard ran for a discovered peer with no hosts.toml row).
var errNoWizardCorrection = errors.New("no configured host to correct")

// wizardHostMatches returns the indexes of the configured hosts the
// wizard target `t` was built from: same address and effective SSH
// port, and the same user — falling back to user-less rows (whose
// wizard user was the local-$USER guess) only when no row names t.User.
func wizardHostMatches(hosts []config.Host, t sshsetup.Target) []int {
	var exact, userless []int
	for i, h := range hosts {
		if !strings.EqualFold(h.Address, t.Host) || h.EffectiveSSHPort() != sshPortOr22(t.Port) {
			continue
		}
		switch h.User {
		case t.User:
			exact = append(exact, i)
		case "":
			userless = append(userless, i)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return userless
}

// sshPortOr22 normalizes an unset (0) SSH port to the openssh default.
func sshPortOr22(p int) int {
	if p == 0 {
		return 22
	}
	return p
}

// networkHostShortName returns the leading dotted-label of a host
// string ("sputnik" from "sputnik.tail-1234.ts.net"). Used so
// auto-generated host names ("bob@sputnik") don't drag the full
// MagicDNS suffix.
func networkHostShortName(host string) string {
	if i := strings.Index(host, "."); i > 0 {
		return host[:i]
	}
	return host
}

// hostExistsByName is a tiny duplicate-guard.
func hostExistsByName(cfg config.Config, name string) bool {
	for _, h := range cfg.Hosts {
		if h.Name == name {
			return true
		}
	}
	return false
}

// remoteAttachTargetFromErr classifies an attachExitedMsg error and
// returns a sshsetup.Target when the failure looks like SSH auth.
// Returns nil for any of: nil error, local attach (no target on
// the msg), an error that doesn't smell like auth (e.g. tmux
// "session not found"), or a Tailscale-SSH peer (where installing
// a key wouldn't help — that's an ACL problem, not a missing key).
// Callers only invoke the wizard on a non-nil return.
func remoteAttachTargetFromErr(msg attachExitedMsg) *sshsetup.Target {
	if msg.Err == nil || msg.RemoteSSHTarget == nil {
		return nil
	}
	// Tailscale SSH peers don't need the wizard: auth is identity-
	// based, no authorized_keys to install. A failure here is an
	// ACL or session-policy issue and needs human attention.
	if msg.RemoteSSHTarget.TailscaleSSH {
		return nil
	}
	// ssh / mosh both surface auth failures as exit 255 with
	// "Permission denied" in stderr. tea.ExecProcess folds stderr
	// into the error string for us (the inner *exec.ExitError
	// Stderr field is the last 64 bytes), so a substring check
	// catches it. Other exit-255 cases (e.g. host key mismatch)
	// produce different strings and route to a generic toast.
	s := strings.ToLower(msg.Err.Error())
	if !strings.Contains(s, "permission denied") &&
		!strings.Contains(s, "publickey") &&
		!strings.Contains(s, "exit status 255") {
		return nil
	}
	rt := msg.RemoteSSHTarget
	return &sshsetup.Target{User: rt.User, Host: rt.Host, Port: rt.Port}
}
