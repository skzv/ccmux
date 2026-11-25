package tui

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/remoteattach"
)

// attachSelectedSession is Enter on Sessions screen.
//
// Three behaviors:
//  1. Local session, we're NOT inside tmux → exec `tmux attach-session`,
//     Bubble Tea is suspended until the user detaches.
//  2. Local session, we ARE inside tmux ($TMUX set, e.g. when running
//     from inside the outer "ccmux" tmux session on mobile) → call
//     `tmux switch-client -t <name>` which doesn't nest sessions and
//     lets `prefix L` jump back to ccmux.
//  3. Remote session → exec `mosh <host> -- tmux attach -t <name>`.
//
// Before any of these, we apply ccmux's chrome (custom status bar) to
// the target session so the attached view shows project name + detach
// hint + Moshi reachability indicator.
func (a App) attachSelectedSession() (App, tea.Cmd) {
	sel := a.sessionsM.Selected()
	if sel == nil {
		return a, nil
	}
	if dbg := debugLogger(); dbg != nil {
		names := make([]string, len(a.sessions))
		for i, s := range a.sessions {
			names[i] = s.Name
		}
		dbg.Printf("attachSelectedSession: cursor=%d name=%q all=%v",
			a.sessionsM.cursor, sel.Name, names)
	}

	// Local sessions resolved by the helper that handles the
	// nested-tmux case. Only rows refresh marked local count: a peer
	// that shares this machine's hostname is still a remote host.
	if isLocalSessionHost(sessionHostLabel(*sel)) {
		label := sel.Project
		if label == "" {
			label = sel.Name
		}
		tick := a.startAttaching(attachKindAttach, label)
		return a, tea.Batch(tick, a.localAttachCmd(sel.Name, sel.Project))
	}

	// Explicit cfg.Hosts entries carry full SSH/Mosh details.
	for i := range a.cfg.Hosts {
		if a.cfg.Hosts[i].Name == sessionHostLabel(*sel) {
			h := &a.cfg.Hosts[i]
			target := h.Address
			if h.User != "" {
				target = h.User + "@" + h.Address
			}
			remoteCmd := remoteTmuxAttach(sel.Name, a.cfg.Sessions.DetachOthersOnAttach())
			tick := a.startAttaching(attachKindRemote, sel.Host)
			rt := &attachRemoteTarget{User: h.User, Host: h.Address, Port: h.EffectiveSSHPort()}
			var (
				cmd   *exec.Cmd
				label string
			)
			if h.Mosh {
				label = "mosh " + target
				cmd = remoteattach.Mosh(target, remoteCmd, h.EffectiveSSHPort())
			} else {
				label = "ssh " + target
				cmd = remoteattach.SSH(target, remoteCmd, h.EffectiveSSHPort())
			}
			return a, tea.Batch(tick, execAttach(cmd, label, rt, false))
		}
	}

	// Auto-discovered tailnet peers don't appear in cfg.Hosts. Look
	// them up in the live a.hosts slice. Prefer the discovered
	// peer's DialHost (a MagicDNS short name) over the bare tailnet
	// IP so existing `known_hosts` entries match — dialing by IP
	// otherwise prompts the user to re-accept a fingerprint they've
	// already trusted.
	//
	// We default to ssh (not mosh) for discovered peers because mosh
	// requires `mosh-server` on the remote, which isn't guaranteed
	// just because Tailscale + ccmuxd are running. Users who want
	// mosh + roaming can pin the host with `ccmux host add --mosh`
	// to override.
	//
	// PATH handling: ssh runs the remote command via the user's
	// login shell in NON-LOGIN NON-INTERACTIVE mode, so /etc/profile
	// and ~/.zprofile/~/.zshrc don't run. Homebrew lives in
	// /opt/homebrew/bin on Apple Silicon and /usr/local/bin on
	// Intel — neither is in the default ssh PATH. We prepend both
	// inline so `tmux` resolves regardless of the remote user's
	// shell config. (An earlier `bash -lc` attempt didn't work
	// because most users put `eval $(brew shellenv)` in their zshrc,
	// not their zprofile.)
	for _, hs := range a.hosts {
		if hs.key() == sessionHostLabel(*sel) && hs.Discovered {
			dial := hs.DialHost
			if dial == "" {
				dial = dialAddrFor(hs)
			}
			if dial == "" {
				return a, func() tea.Msg {
					return toastMsg{Text: fmt.Sprintf(tr("no reachable address for %s"), sel.Host), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
				}
			}
			remoteCmd := remoteTmuxAttach(sel.Name, a.cfg.Sessions.DetachOthersOnAttach())
			cmd := remoteattach.SSH(dial, remoteCmd, hs.SSHPort)
			if dbg := debugLogger(); dbg != nil {
				dbg.Printf("attach discovered: ssh -t %s port=%d %q", dial, hs.SSHPort, remoteCmd)
			}
			tick := a.startAttaching(attachKindRemote, sel.Host)
			// Strip any "user@" prefix on dial so we can pass it
			// as Host to the wizard. Honor the host row's SSHPort
			// when set; otherwise fall back to SSH 22 since dial
			// is just a hostname here. Propagate TailscaleSSH so
			// the post-attach auto-route skips the wizard for
			// TS-SSH peers (where installing a key wouldn't help).
			port := hs.SSHPort
			if port == 0 {
				port = 22
			}
			rt := &attachRemoteTarget{Host: dial, Port: port, TailscaleSSH: hs.TailscaleSSH}
			if i := strings.Index(dial, "@"); i >= 0 {
				rt.User = dial[:i]
				rt.Host = dial[i+1:]
			}
			return a, tea.Batch(tick, execAttach(cmd, "ssh "+dial, rt, false))
		}
	}

	return a, func() tea.Msg {
		return toastMsg{Text: fmt.Sprintf(tr("no host config for %s"), sel.Host), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
	}
}

// dialAddrFor extracts the bare host (no port) from a discovered
// peer's Address. Discovered Address is "<tailnet-ip>:<port>" — mosh
// wants just the host. Returns "" if there's nothing usable.
func dialAddrFor(hs hostStatus) string {
	if hs.Address == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(hs.Address); err == nil {
		return host
	}
	return hs.Address
}

// shellQuote escapes `s` for safe interpolation inside a POSIX
// single-quoted string. The remote attach builds a single command
// string (PATH=... tmux attach -t '<name>') that's passed to ssh,
// which executes it via the remote user's shell. The session name
// could in theory contain characters the shell would expand; quoting
// it defensively keeps a pathological project basename from
// breaking out. ccmux's own session names are tame (alphanumeric +
// dash + underscore from SessionNameForPath), but belt-and-suspenders.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// remoteTmuxAttach builds the single-string command we hand to ssh
// for attaching to a remote tmux session. ssh runs it via the user's
// shell in NON-LOGIN NON-INTERACTIVE mode, so /etc/profile + zshrc/
// zprofile don't fire and Homebrew-installed tmux isn't on PATH by
// default. The prepended paths cover the common install locations
// on both ends of the wire:
//
//	/opt/homebrew/bin                    — macOS Apple Silicon Homebrew
//	/usr/local/bin                       — macOS Intel Homebrew + Linux convention
//	/home/linuxbrew/.linuxbrew/bin       — Linuxbrew on Linux
//	/snap/bin                            — Snap-installed tmux on Linux
//
// Non-existent paths in the list are silently ignored by the shell,
// so this is safe to include unconditionally regardless of whether
// the dialer or the target is macOS or Linux. The trailing $PATH
// preserves whatever else the remote shell already had set up.
//
// detachOthers carries the local user's attach-mode preference onto
// the remote tmux: in mirror mode the remote session keeps any other
// clients (the remote machine's own terminal, another of the user's
// devices); in exclusive mode it kicks them. The preference is the
// attaching CLIENT's — "do I want to bump whoever else is viewing
// this" — so the local config is the right source.
//
// The target is tmux's exact form `=name:` (see tmux.AttachArgs): a
// bare `-t name` resolves by prefix and fnmatch, so it could land in
// c-foo-app when c-foo was gone, and read api.v2 as session "api",
// pane "v2".
func remoteTmuxAttach(session string, detachOthers bool) string {
	flags := ""
	if detachOthers {
		flags = " -d"
	}
	return "PATH=/opt/homebrew/bin:/usr/local/bin:/home/linuxbrew/.linuxbrew/bin:/snap/bin:$PATH" +
		" tmux attach-session" + flags + " -t " + shellQuote("="+session+":")
}

// remoteNewSessionAttachProcess builds the ssh/mosh process for a session
// ccmux just created on a remote host. It deliberately forces mirror attach
// so creating a new session never detaches existing remote tmux clients.
func remoteNewSessionAttachProcess(msg remoteSessionStartedMsg) (*exec.Cmd, string, string) {
	target := msg.DialHost
	if msg.User != "" {
		target = msg.User + "@" + msg.DialHost
	}
	remoteCmd := remoteTmuxAttach(msg.SessionName, false)
	// msg.SSHPort is threaded from the create-session forms. Dropping
	// it here was the last gap in that chain: the session was created
	// on the right host and port, then the follow-up attach dialed 22.
	if msg.Mosh {
		return remoteattach.Mosh(target, remoteCmd, msg.SSHPort), target, remoteCmd
	}
	return remoteattach.SSH(target, remoteCmd, msg.SSHPort), target, remoteCmd
}

// localAttachCmd builds the tea.Cmd that suspends Bubble Tea, applies
// ccmux's chrome to the target session, then either switch-clients
// (when we're already inside the outer ccmux tmux session, the mobile
// flow) or attach-sessions (when we're in a bare terminal). One
// definition shared by the Sessions screen and the Projects screen
// so both paths handle the nested-tmux case identically — Projects
// previously always called attach-session, which silently failed
// inside the outer ccmux session.

// localAttachCmd kicks off an explicit existing-session attach, honoring
// sessions.attach_mode. Create-then-attach flows use
// localNewSessionAttachCmd so opening a newly-created session never
// detaches existing tmux clients.
func (a App) localAttachCmd(session, projectLabel string) tea.Cmd {
	return prepLocalAttachCmd(session, projectLabel, a.cfg.Sessions.DetachOthersOnAttach())
}

// localNewSessionAttachCmd kicks off a local attach after ccmux has just
// created the session. It shares the same prep pipeline as localAttachCmd
// so nested-tmux switch-client behavior and chrome application stay
// centralized, but forces mirror attach.
func (a App) localNewSessionAttachCmd(session, projectLabel string) tea.Cmd {
	return prepLocalAttachCmd(session, projectLabel, false)
}

// refreshAfterDetachMsg fires after the TUI resumes from tmux attach;
// triggers fresh data load so the screen is current.
type refreshAfterDetachMsg struct{}
