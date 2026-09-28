package cmd

// --host for the session commands (`attach`, `kill`, `rename`, `list`):
// act on a configured remote host's sessions through its ccmuxd, the
// way the TUI routes a remote row's attach / `x` / `R` to the host it
// came from. Without --host (or with --host local) they act on this
// machine, as before.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/remoteattach"
	"github.com/skzv/ccmux/internal/tmux"
)

// hostFlagUsage is the --host help shared by the session commands.
const hostFlagUsage = "configured host (`ccmux host list`) whose sessions to act on; empty or \"local\" for this machine"

// remoteOpTimeout bounds one remote daemon round trip made by a CLI
// session command.
const remoteOpTimeout = 10 * time.Second

// isLocalHost reports whether a --host value means this machine.
func isLocalHost(host string) bool {
	host = strings.TrimSpace(host)
	return host == "" || host == "local"
}

// remoteHost is a --host resolved against the config: the host entry
// and a client for its ccmuxd.
type remoteHost struct {
	name string // as given to --host
	cfg  config.Host
	addr string // its ccmuxd, "<address>:<port>"
	cli  *daemon.Client
}

// lookupHost finds host among the configured hosts. Only configured
// hosts are reachable from the CLI, as for `ccmux shell --host` and
// `ccmux notes --host`: a typo must never fall back to this machine.
func lookupHost(cfg config.Config, host string) (remoteHost, error) {
	host = strings.TrimSpace(host)
	for _, h := range cfg.Hosts {
		if h.Name == host {
			addr := hostDaemonAddr(cfg, h)
			return remoteHost{name: host, cfg: h, addr: addr, cli: daemon.RemoteClient(addr)}, nil
		}
	}
	return remoteHost{}, fmt.Errorf("unknown host %q — configure it with `ccmux host add`", host)
}

// hostDaemonAddr is the "<address>:<port>" of a configured host's
// ccmuxd: its own port, else daemon.tailnet_port, else 7474 — what the
// TUI dials.
func hostDaemonAddr(cfg config.Config, h config.Host) string {
	port := h.Port
	if port == 0 {
		port = cfg.Daemon.TailnetPort
	}
	if port == 0 {
		port = defaultTailnetPort
	}
	return fmt.Sprintf("%s:%d", h.Address, port)
}

// remoteErr explains a failed call to rh's daemon: that the host
// couldn't be reached at all, or what its daemon answered.
func (rh remoteHost) remoteErr(what string, err error) error {
	if daemon.StatusCode(err) == 0 {
		// A refused or failed dial reads best as just its cause
		// ("connect: connection refused"): the client's own wrapping
		// repeats the address twice more.
		var op *net.OpError
		if errors.As(err, &op) && op.Err != nil {
			return fmt.Errorf("can't reach ccmuxd on %s (%s): %v", rh.name, rh.addr, op.Err)
		}
		return fmt.Errorf("can't reach ccmuxd on %s (%s): %w", rh.name, rh.addr, err)
	}
	return fmt.Errorf("%s on %s: %w", what, rh.name, err)
}

// sessions lists rh's sessions as tmux.Session values (name + directory,
// all resolveKillTarget / projectSession need).
func (rh remoteHost) sessions(ctx context.Context) ([]daemon.SessionState, []tmux.Session, error) {
	ss, err := rh.cli.Sessions(ctx)
	if err != nil {
		return nil, nil, rh.remoteErr("list sessions", err)
	}
	out := make([]tmux.Session, len(ss))
	for i, s := range ss {
		out[i] = tmux.Session{Name: s.Name, Path: s.Path}
	}
	return ss, out, nil
}

// checkRemoteProjectArg refuses a relative path for --host: it would
// name a directory on this machine, not on the host.
func checkRemoteProjectArg(host, arg string) error {
	if strings.Contains(arg, "/") && !path.IsAbs(arg) {
		return fmt.Errorf("%q is a path on this machine; with --host give a session name, a project name, or an absolute path on %s", arg, host)
	}
	return nil
}

// projectDir maps a kill argument to a project directory on rh: an
// absolute path is one; a bare name is the project of that name the
// host's daemon lists (GET /v1/projects). found is false when the host
// has no such project — then only the plain session name can match.
func (rh remoteHost) projectDir(ctx context.Context, arg string) (string, bool) {
	if path.IsAbs(arg) {
		return path.Clean(arg), true
	}
	if ps, err := rh.cli.Projects(ctx); err == nil {
		for _, p := range ps {
			if p.Name == arg {
				return p.Path, true
			}
		}
	}
	return arg, false
}

// runRemoteKill is `ccmux kill --host`: the argument resolves as for a
// local kill — a session of that name, else the project's own session
// (by directory, among the host's sessions) — and the host's daemon
// kills it.
func runRemoteKill(ctx context.Context, w io.Writer, cfg config.Config, host, arg string) error {
	if err := refuseSessionID("kill", arg); err != nil {
		return err
	}
	if err := checkRemoteProjectArg(host, arg); err != nil {
		return err
	}
	rh, err := lookupHost(cfg, host)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, remoteOpTimeout)
	defer cancel()
	_, sessions, err := rh.sessions(ctx)
	if err != nil {
		return err
	}
	name, err := resolveKillTarget(arg, sessions, func(a string) (string, bool) { return rh.projectDir(ctx, a) }, "ccmux kill --host "+shellWord(rh.name))
	if err != nil {
		return fmt.Errorf("host %s: %w", rh.name, err)
	}
	if err := rh.cli.Kill(ctx, name); err != nil {
		if daemon.StatusCode(err) == http.StatusNotFound {
			return fmt.Errorf("no session %q on %s (it may have just ended)", name, rh.name)
		}
		return rh.remoteErr("kill "+name, err)
	}
	fmt.Fprintf(w, "killed %s on %s\n", safeField(name), safeField(rh.name))
	return nil
}

// runRemoteRename is `ccmux rename --host`: the host's daemon renames
// the session (the names were checked as for a local rename).
func runRemoteRename(ctx context.Context, w io.Writer, cfg config.Config, host, oldName, newName string) error {
	rh, err := lookupHost(cfg, host)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, remoteOpTimeout)
	defer cancel()
	if err := rh.cli.Rename(ctx, oldName, newName); err != nil {
		switch daemon.StatusCode(err) {
		case http.StatusNotFound:
			return fmt.Errorf("no session %q on %s", oldName, rh.name)
		case http.StatusConflict:
			return fmt.Errorf("%s already has a session named %q", rh.name, newName)
		}
		return rh.remoteErr("rename "+oldName, err)
	}
	fmt.Fprintf(w, "renamed %s → %s on %s\n", safeField(oldName), safeField(newName), safeField(rh.name))
	return nil
}

// runRemoteList is `ccmux list --host`: the host's sessions, with the
// state its daemon classified them in.
func runRemoteList(ctx context.Context, cfg config.Config, host string) ([]daemon.SessionState, error) {
	rh, err := lookupHost(cfg, host)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, remoteOpTimeout)
	defer cancel()
	ss, _, err := rh.sessions(ctx)
	if err != nil {
		return nil, err
	}
	for i := range ss {
		ss[i].Host = rh.name // the daemon calls its own sessions "local"
	}
	return ss, nil
}

// runRemoteAttach is `ccmux attach --host`: a session of that name on
// the host, else the project's session there — started by the host's
// daemon when it isn't running (POST /v1/sessions, which finds the
// project's own session by directory) — then attached over ssh, or mosh
// when the host is configured for it, the way the TUI attaches a remote
// row. Without a terminal it prints how to attach instead.
func runRemoteAttach(ctx context.Context, w io.Writer, cfg config.Config, host, arg string) error {
	if arg == "" {
		return errors.New("with --host, name the session or project to attach to — the current directory is on this machine")
	}
	if err := refuseSessionID("attach", arg); err != nil {
		return err
	}
	if err := checkRemoteProjectArg(host, arg); err != nil {
		return err
	}
	rh, err := lookupHost(cfg, host)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	listed, _, err := rh.sessions(ctx)
	if err != nil {
		return err
	}
	running := func(name string) bool {
		for _, s := range listed {
			if s.Name == name {
				return true
			}
		}
		return false
	}
	session := ""
	if tmux.CheckTarget(arg) == nil && running(arg) {
		session = arg
	} else {
		req := daemon.NewSessionRequest{Project: arg, Continue: true}
		if path.IsAbs(arg) {
			req = daemon.NewSessionRequest{Project: path.Base(path.Clean(arg)), Path: arg, Continue: true}
		} else if !isBareProjectName(arg) {
			return fmt.Errorf("%q is not a session or project name", arg)
		}
		st, err := rh.cli.NewSession(ctx, req)
		if err != nil {
			return rh.remoteErr("start "+arg, err)
		}
		session = st.Name
	}
	// A session's name comes back from the peer: the attach below
	// targets it, so it must be a target that reaches only it.
	if err := tmux.CheckTarget(session); err != nil {
		return fmt.Errorf("%s answered with session %q: %w", rh.name, session, err)
	}
	created := !running(session)
	if !stdinIsTTY() {
		verb := "is running"
		if created {
			verb = "started"
		}
		fmt.Fprintf(w, "%s %s on %s; attach from a terminal with: ccmux attach --host %s %s\n",
			safeField(session), verb, safeField(rh.name), shellWord(rh.name), shellWord(session))
		return nil
	}
	return runForeground(remoteAttachCmd(rh.cfg, session, detachOthersForAttachIntent(created)))
}

// remoteAttachCmd is the ssh (or, for a host configured with mosh,
// mosh) command that attaches this terminal to session on host h, with
// the host's login user and ssh port. detachOthers is this machine's
// attach-mode preference, as for a local attach.
func remoteAttachCmd(h config.Host, session string, detachOthers bool) *exec.Cmd {
	remoteCmd := remoteTmuxAttach(session, detachOthers)
	if h.Mosh {
		target := h.Address
		if h.User != "" {
			target = h.User + "@" + h.Address
		}
		return remoteattach.Mosh(target, remoteCmd, h.EffectiveSSHPort())
	}
	// nocontext: foreground interactive ssh; it ends when the user detaches.
	return exec.Command("ssh", shellSSHArgs(h, remoteCmd)...)
}
