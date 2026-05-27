package tui

import (
	"fmt"
	"strings"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/sshsetup"
)

// persistWizardAdded writes new user@host entries to hosts.toml
// after the user confirmed the multi-select on the wizard's
// enumerate step. Persists the existing app config plus the new
// rows. Errors are surfaced as toasts rather than returned because
// the wizard has already done the irreversible part (installed the
// key); a config-save failure is recoverable next time the user
// runs the wizard.
//
// The added rows reuse the target's address + port; the only
// difference is the user. Host names are derived from
// "<user>@<short-name>" via networkHostShortName so they read
// reasonably in `ccmux host list`.
func persistWizardAdded(a App, target sshsetup.Target, addedUsers []string) App {
	if len(addedUsers) == 0 {
		return a
	}
	cfg := a.cfg
	shortHost := networkHostShortName(target.Host)
	for _, u := range addedUsers {
		name := fmt.Sprintf("%s@%s", u, shortHost)
		// Skip if the name is already present — re-running the
		// wizard shouldn't create duplicate rows.
		if hostExistsByName(cfg, name) {
			continue
		}
		cfg.Hosts = append(cfg.Hosts, config.Host{
			Name:    name,
			Address: target.Host,
			User:    u,
			Port:    target.Port,
			Mosh:    true,
		})
	}
	if err := config.Save(cfg); err != nil {
		// Stash the error onto the model — keep the rest of the
		// flow successful. We deliberately don't fire a toast
		// here; the caller already emits a success toast.
		if dbg := debugLogger(); dbg != nil {
			dbg.Printf("persist wizard-added users: %v", err)
		}
		return a
	}
	a.cfg = cfg
	return a
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
// the msg), or an error that doesn't smell like auth (e.g. tmux
// "session not found"). Callers only invoke the wizard on a
// non-nil return.
func remoteAttachTargetFromErr(msg attachExitedMsg) *sshsetup.Target {
	if msg.Err == nil || msg.RemoteSSHTarget == nil {
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
