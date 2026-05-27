// Package sshsetup handles the one-time SSH bootstrap a user needs
// before ccmux can attach to a remote ccmuxd over Tailscale.
//
// The problem this package solves: Tailscale gives you a stable
// `100.x.x.x` address, but `mosh` / `ssh` still need normal key auth
// against the remote sshd. The first time you try to attach to a new
// host you see `Permission denied (publickey)` and have to remember to
// run `ssh-copy-id` by hand. That's the friction this package removes.
//
// Public surface:
//
//   - Probe()                 — non-interactive auth check, returns a
//                                structured ProbeResult so callers can
//                                show a specific error instead of the
//                                raw sshd stderr.
//   - EnsureLocalKey()        — reuse an existing ~/.ssh key (ed25519
//                                preferred, then rsa); generate a
//                                passphrase-less ed25519 only if
//                                nothing usable is present.
//   - InstallKeyViaPassword() — connect once with a password, append
//                                our pubkey to remote authorized_keys
//                                (idempotent, dedupe by content), fix
//                                perms, validate the install by
//                                reconnecting with key auth.
//   - EnumerateUsers()        — after a successful install, list the
//                                other real Unix accounts on the
//                                remote so the UI can offer them as
//                                additional host entries.
//
// The package never prompts the user directly — it returns enough
// information for the TUI wizard or the `ccmux host setup-ssh` CLI
// to drive the interaction. Password values flow in as plain strings
// and are never stored, logged, or retained past the call.
package sshsetup

import (
	"fmt"
	"strings"
)

// Target identifies a remote SSH endpoint. Zero User defaults to the
// local $USER at the call site; zero Port defaults to 22.
type Target struct {
	User string
	Host string
	Port int
}

// Addr returns "host:port", filling in 22 when Port is 0.
func (t Target) Addr() string {
	p := t.Port
	if p == 0 {
		p = 22
	}
	return fmt.Sprintf("%s:%d", t.Host, p)
}

// UserOr returns t.User, or fallback if t.User is blank. Used so the
// caller doesn't have to special-case empty users at every site.
func (t Target) UserOr(fallback string) string {
	if strings.TrimSpace(t.User) == "" {
		return fallback
	}
	return t.User
}

// String renders the target in the canonical "user@host:port" form a
// human reads in TUI banners. Port is omitted when 22.
func (t Target) String() string {
	u := t.User
	if u == "" {
		u = "<user>"
	}
	if t.Port == 0 || t.Port == 22 {
		return fmt.Sprintf("%s@%s", u, t.Host)
	}
	return fmt.Sprintf("%s@%s:%d", u, t.Host, t.Port)
}

// Progress is an optional UI callback. The TUI wizard renders each
// stage as a status line; the CLI prints them as bullets. Both pass a
// non-nil Progress; for tests, nil is fine and ignored.
type Progress func(stage, detail string)

func (p Progress) report(stage, detail string) {
	if p != nil {
		p(stage, detail)
	}
}
