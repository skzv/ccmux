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

// SSH builds `ssh -t target remoteCmd`. The -t allocates a PTY so
// tmux on the remote end sees a terminal. remoteCmd is passed as a
// single argv element so the remote shell parses it as one command;
// callers are responsible for any quoting inside that string.
func SSH(target, remoteCmd string) *exec.Cmd {
	return exec.Command("ssh", "-t", target, remoteCmd)
}

// SSHInteractive builds `ssh -t target` with no command — drops the
// user at a remote login shell. Used by the Network screen's "open
// shell on peer" action.
func SSHInteractive(target string) *exec.Cmd {
	return exec.Command("ssh", "-t", target)
}

// Mosh builds `mosh target -- bash -c remoteCmd`. mosh doesn't take
// a remote command as a single positional like ssh; it execs argv
// after the `--`, so we wrap in `bash -c` for the shell parsing the
// remoteCmd string expects (parens, redirects, quoting).
func Mosh(target, remoteCmd string) *exec.Cmd {
	return exec.Command("mosh", target, "--", "bash", "-c", remoteCmd)
}

// SSHRunArgv builds `ssh target -- ARGV...`, used when the caller
// already has a remote argv (e.g. the dashboard's explicit-host
// attach builds tmux.AttachArgs and wants to run that). Picks ssh
// or mosh based on `useMosh`.
func RunArgv(target string, useMosh bool, argv []string) *exec.Cmd {
	bin := "ssh"
	if useMosh {
		bin = "mosh"
	}
	return exec.Command(bin, append([]string{target, "--"}, argv...)...)
}
