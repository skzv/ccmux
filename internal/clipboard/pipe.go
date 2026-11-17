package clipboard

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// PipeSelection routes a tmux selection to the local OS clipboard when
// — and only when — the selection was made on a *local* tmux client.
// Remote clients (SSH/Mosh) keep relying on the OSC 52 path that
// ccmuxd already wires via `set-clipboard on`; this function never
// targets a remote machine's clipboard.
//
// The motivating case: Terminal.app on macOS silently drops OSC 52
// writes, so a user sitting at the daemon machine has no copy path
// without this. But the same daemon may also serve a remote client
// over Tailscale/Mosh; for that client, OSC 52 *is* the right path
// because it travels back over the SSH pipe to whatever device the
// human is actually looking at. Running pbcopy for a phone's copy
// would silently poison the daemon machine's clipboard.
//
// copier is the `--client` value the tmux binding passes: the tty of
// the client that made the selection (#{client_tty}, see PipeCommand).
// When it names an attached client of this tmux server, that client
// alone decides: local → pipe, remote → don't. Otherwise — no flag (a
// binding from an older ccmux, or the user's own), an empty value
// (tmux before 3.0 expands client formats in copy-pipe to ""), the
// literal `#{client_tty}` (a tmux that didn't expand it), or a tty
// that isn't one of this server's clients — the copier is unknown and
// the old rule applies: pipe when at least one attached client is
// local. The value is only ever compared with tmux's own client list,
// never opened, so argv can't point this at an arbitrary file.
//
// If we can't decide (tmux query failed, no clients), default to NOT
// piping — the OSC 52 path is the safer side of the trade-off because
// it never poisons the wrong clipboard.
//
// `deps` carries all OS / tmux interactions so tests can pin the full
// matrix without spinning up a tmux server or forking processes. The
// zero value works in production (Deps{}.fill() resolves all hooks).
func PipeSelection(ctx context.Context, in io.Reader, copier string, deps Deps) error {
	deps = deps.fill()

	// Drain stdin into memory first: selections are small (mouse drags
	// rarely exceed a few KB), and an empty one — tmux firing the
	// binding on a click with no drag — needs no tmux query at all.
	payload, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("read selection: %w", err)
	}
	if len(payload) == 0 {
		return nil
	}

	// A failed query leaves clients empty, which routes nowhere: no
	// local clipboard we can vouch for. OSC 52 has already handled the
	// remote case if any.
	clients, _ := deps.ListTmuxClients(ctx)
	if !copyIsLocal(copier, clients, deps.IsClientSSH) {
		return nil
	}

	pipe := deps.NativeClipboardTool()
	if len(pipe) == 0 {
		// macOS without pbcopy on PATH, or a Linux box without wl-copy/
		// xclip. Local user is out of luck for the system clipboard via
		// this path, but the binding still cancels cleanly.
		return nil
	}

	if err := runPipe(ctx, pipe, bytes.NewReader(payload)); err != nil {
		return fmt.Errorf("pipe to %s: %w", pipe[0], err)
	}
	return nil
}

// runPipe execs argv with stdin = selection, stderr = our stderr, and a
// short timeout so a hung clipboard tool can't block tmux forever.
func runPipe(ctx context.Context, argv []string, in io.Reader) error {
	if len(argv) == 0 {
		return fmt.Errorf("empty argv")
	}
	pipeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(pipeCtx, argv[0], argv[1:]...)
	cmd.Stdin = in
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// copyIsLocal decides whether a selection belongs on this machine's
// clipboard. copier is the raw --client value (see PipeSelection);
// isRemote reports whether a client PID belongs to someone on another
// device.
func copyIsLocal(copier string, clients []TmuxClient, isRemote func(pid int) bool) bool {
	if c, ok := copyingClient(copier, clients); ok {
		return !isRemote(c.PID)
	}
	for _, c := range clients {
		if !isRemote(c.PID) {
			return true
		}
	}
	return false
}

// copyingClient resolves copier to the attached client whose tty it
// names. ok is false when copier carries no usable value — absent,
// empty, or an unexpanded `#{…}` format — or names a tty that isn't a
// client of this tmux server (one that detached since the copy, or a
// value that never came from tmux).
func copyingClient(copier string, clients []TmuxClient) (TmuxClient, bool) {
	tty := strings.TrimSpace(copier)
	if tty == "" || strings.Contains(tty, "#{") {
		return TmuxClient{}, false
	}
	for _, c := range clients {
		if c.TTY == tty {
			return c, true
		}
	}
	return TmuxClient{}, false
}

// TmuxClient is the minimum subset of `tmux list-clients` output the
// dispatch logic needs: the PID, to inspect the client process's env
// for SSH_CONNECTION, and the tty, to match the client the copy-mode
// binding names.
type TmuxClient struct {
	PID int
	TTY string
}

// Deps is the dependency-injection seam for PipeSelection. Production
// code passes Deps{} and the zero value picks the real implementations.
// Tests pass Deps{ListTmuxClients: ..., IsClientSSH: ..., NativeClipboardTool: ...}.
type Deps struct {
	// ListTmuxClients returns the currently-attached tmux clients on
	// the daemon's tmux server. nil/empty when none.
	ListTmuxClients func(ctx context.Context) ([]TmuxClient, error)

	// IsClientSSH reports whether the given PID's process environment
	// contains SSH_CONNECTION (the canonical "this shell came in via
	// SSH" signal). false on any error reading env so we err toward
	// "treat as local", which is the safer side of the trade-off when
	// the client is *probably* local but we couldn't verify.
	IsClientSSH func(pid int) bool

	// NativeClipboardTool returns the argv for the OS's clipboard
	// reader, or nil/empty when nothing is available. Resolved once
	// per invocation rather than at package init so a user installing
	// wl-copy mid-session takes effect on the next copy.
	NativeClipboardTool func() []string
}

func (d Deps) fill() Deps {
	if d.ListTmuxClients == nil {
		d.ListTmuxClients = listTmuxClients
	}
	if d.IsClientSSH == nil {
		d.IsClientSSH = isClientSSH
	}
	if d.NativeClipboardTool == nil {
		d.NativeClipboardTool = nativeClipboardTool
	}
	return d
}

// listClientsFormat is the `tmux list-clients -F` format
// parseTmuxClientList reads: PID, a space, tty.
const listClientsFormat = "#{client_pid} #{client_tty}"

// listTmuxClients queries `tmux list-clients` for each client's PID
// and tty, one client per line. A failed query (no tmux server,
// permission denied, etc.) returns an empty slice — callers treat that
// as "no local client we can detect" which falls through to the safe
// default.
func listTmuxClients(ctx context.Context) ([]TmuxClient, error) {
	cmd := exec.CommandContext(ctx, "tmux", "list-clients", "-F", listClientsFormat)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseTmuxClientList(out), nil
}

// parseTmuxClientList turns listClientsFormat output into a slice of
// TmuxClient. Split out for unit testing — the exec call in
// listTmuxClients is hard to mock without standing up a real tmux,
// but the parser is pure bytes-in / slice-out. A line with a PID and
// no tty (a client without one) still counts; it just can't be named
// by --client.
func parseTmuxClientList(out []byte) []TmuxClient {
	var clients []TmuxClient
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue // skip malformed lines rather than fail the whole query
		}
		c := TmuxClient{PID: pid}
		if len(fields) > 1 {
			c.TTY = fields[1]
		}
		clients = append(clients, c)
	}
	return clients
}

// isClientSSH inspects a process's environment for SSH_CONNECTION.
// Cross-platform via two read paths:
//
//   - Linux: /proc/<pid>/environ is a NUL-separated dump. Free to read
//     when we own the process (we do — tmux clients are our user's).
//   - macOS: no /proc; `ps -E -p <pid>` shows env. `-E` is macOS-
//     specific (on Linux it means "exec"), so the runtime.GOOS split
//     is load-bearing.
//
// Unknown OS or read error returns false — see Deps doc comment for
// the "treat as local on unknown" rationale.
func isClientSSH(pid int) bool {
	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
		if err != nil {
			return false
		}
		return envMarksRemote(b)
	case "darwin":
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		out, err := exec.CommandContext(ctx, "ps", "-E", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return false
		}
		return envMarksRemote(out)
	}
	return false
}

// RemoteClientEnv marks a tmux client that ccmuxd itself spawned on
// behalf of a remote device (the attach WebSocket's PTY). Such a client
// is local as far as the process tree goes — no SSH_CONNECTION — but
// the human is on a phone, so a copy must not land in this machine's
// clipboard.
const RemoteClientEnv = "CCMUX_REMOTE_CLIENT"

// envMarksRemote reports whether a dumped process environment belongs
// to a client whose user is on another device.
func envMarksRemote(env []byte) bool {
	return bytes.Contains(env, []byte("SSH_CONNECTION=")) ||
		bytes.Contains(env, []byte(RemoteClientEnv+"=1"))
}

// nativeClipboardTool returns the argv for the OS's clipboard reader.
// Wayland before X on Linux. Empty slice when no tool is available.
func nativeClipboardTool() []string {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("pbcopy"); err == nil {
			return []string{"pbcopy"}
		}
	case "linux":
		if _, err := exec.LookPath("wl-copy"); err == nil {
			return []string{"wl-copy"}
		}
		if _, err := exec.LookPath("xclip"); err == nil {
			return []string{"xclip", "-selection", "clipboard"}
		}
	}
	return nil
}
