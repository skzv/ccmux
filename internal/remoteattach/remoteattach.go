// Package remoteattach builds the *exec.Cmd values the TUI hands to
// tea.ExecProcess when foregrounding into a remote session over ssh
// or mosh. Exists so the TUI doesn't shell out directly — every site
// goes through one helper, which keeps the argv shape consistent
// across the dashboard's remote-attach, the bare-session remote
// flow, and the network screen's manual ssh.
package remoteattach

import (
	"os/exec"
)

// defaultPort reports whether a configured SSH port means "just use
// the default" — 0 (unset) or 22. Callers that pass one of those get
// argv identical to the no-port form, so the overwhelmingly common
// case stays byte-for-byte what it always was.
func defaultPort(port int) bool { return port == 0 || port == 22 }

// sshPortFlags returns the `-p N` pair for a non-default port, or nil.
func sshPortFlags(port int) []string {
	if defaultPort(port) {
		return nil
	}
	return []string{"-p", itoa(port)}
}

// moshSSHFlags returns mosh's way of reaching a non-default SSH port,
// or nil for the default.
//
// mosh's own `-p` is the UDP port range for the mosh session — NOT the
// SSH port. Passing `-p 2222` to mosh the way you would to ssh silently
// asks for a UDP bind on 2222 while still connecting to SSH on 22, so
// the fix has to route through `--ssh`.
func moshSSHFlags(port int) []string {
	if defaultPort(port) {
		return nil
	}
	return []string{"--ssh=ssh -p " + itoa(port)}
}

// SSH builds `ssh -t [-p port] target remoteCmd`. The -t allocates a
// PTY so tmux on the remote end sees a terminal. remoteCmd is passed as
// a single argv element so the remote shell parses it as one command;
// callers are responsible for any quoting inside that string.
//
// port is required rather than optional on purpose: the bug this
// replaced was a port-aware variant existing alongside a port-blind one
// and call sites reaching for the wrong shape. Pass 0 when there is
// genuinely no configured port.
func SSH(target, remoteCmd string, port int) *exec.Cmd {
	args := append([]string{"-t"}, sshPortFlags(port)...)
	return exec.Command("ssh", append(args, target, remoteCmd)...)
}

// SSHInteractive builds `ssh -t [-p port] target` with no command —
// drops the user at a remote login shell. Used by the Network screen's
// "open shell on peer" action and the post-SSH-setup "open a shell now"
// flow. Pass 0 when no port is configured.
func SSHInteractive(target string, port int) *exec.Cmd {
	args := append([]string{"-t"}, sshPortFlags(port)...)
	return exec.Command("ssh", append(args, target)...)
}

// itoa is a tiny strconv.Itoa to avoid pulling strconv into this
// otherwise import-light package.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// Mosh builds `mosh [--ssh=...] target -- bash -c remoteCmd`. mosh
// doesn't take a remote command as a single positional like ssh; it
// execs argv after the `--`, so we wrap in `bash -c` for the shell
// parsing the remoteCmd string expects (parens, redirects, quoting).
//
// See moshSSHFlags for why a non-default port becomes `--ssh` and not
// mosh's own `-p`.
func Mosh(target, remoteCmd string, port int) *exec.Cmd {
	args := append(moshSSHFlags(port), target, "--", "bash", "-c", remoteCmd)
	return exec.Command("mosh", args...)
}

// RunArgv builds `ssh|mosh [port flags] target -- ARGV...`, used when
// the caller already has a remote argv (e.g. the dashboard's
// explicit-host attach builds tmux.AttachArgs and wants to run that).
// Picks ssh or mosh based on `useMosh`, and the matching port syntax
// for whichever it picked.
func RunArgv(target string, useMosh bool, port int, argv []string) *exec.Cmd {
	bin, flags := "ssh", sshPortFlags(port)
	if useMosh {
		bin, flags = "mosh", moshSSHFlags(port)
	}
	return exec.Command(bin, append(append(flags, target, "--"), argv...)...)
}
