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
// — and only when — at least one *local* tmux client is currently
// attached. Remote clients (SSH/Mosh) keep relying on the OSC 52 path
// that ccmuxd already wires via `set-clipboard on`; this function
// never targets a remote machine's clipboard.
//
// The motivating case: Terminal.app on macOS silently drops OSC 52
// writes, so a user sitting at the daemon machine has no copy path
// without this. But the same daemon may also serve a remote client
// over Tailscale/Mosh; for that client, OSC 52 *is* the right path
// because it travels back over the SSH pipe to whatever device the
// human is actually looking at. Running pbcopy unconditionally would
// silently poison the daemon machine's clipboard for the remote case.
//
// Resolution: at copy time (each invocation is one mouse-drag-end),
// ask tmux for its current clients, inspect each client's process env
// for SSH_CONNECTION, and only pipe to pbcopy/wl-copy/xclip when at
// least one attached client is local. If we can't decide (tmux query
// failed, no clients, env unreadable), default to NOT piping — the
// OSC 52 path is the safer side of the trade-off because it never
// poisons the wrong clipboard.
//
// `deps` carries all OS / tmux interactions so tests can pin the full
// matrix without spinning up a tmux server or forking processes. The
// zero value works in production (Deps{}.fill() resolves all hooks).
func PipeSelection(ctx context.Context, in io.Reader, deps Deps) error {
	deps = deps.fill()

	clients, queryErr := deps.ListTmuxClients(ctx)
	// Drain stdin into memory first so we can decide where to forward
	// it without losing bytes if the query took a moment. Selections
	// are small (mouse drags rarely exceed a few KB); the OSC 52
	// limit terminals impose is well below 100KB. Reading it all up
	// front is fine.
	payload, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("read selection: %w", err)
	}
	if len(payload) == 0 {
		// Empty selection — nothing to do. Don't even probe.
		return nil
	}

	hasLocalClient := false
	for _, c := range clients {
		if !deps.IsClientSSH(c.PID) {
			hasLocalClient = true
			break
		}
	}
	if !hasLocalClient {
		// Either the tmux query failed (queryErr != nil) or every
		// attached client is remote. Either way, no local clipboard to
		// target. OSC 52 has already handled the remote case if any.
		_ = queryErr // intentionally swallowed; logged in caller if needed
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

// TmuxClient is the minimum subset of `tmux list-clients` output the
// dispatch logic needs. Just the PID — that's enough to inspect the
// client process's env for SSH_CONNECTION.
type TmuxClient struct {
	PID int
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

// listTmuxClients queries `tmux list-clients -F '#{client_pid}'` and
// parses one PID per line. A failed query (no tmux server, permission
// denied, etc.) returns an empty slice — callers treat that as "no
// local client we can detect" which falls through to the safe default.
func listTmuxClients(ctx context.Context) ([]TmuxClient, error) {
	cmd := exec.CommandContext(ctx, "tmux", "list-clients", "-F", "#{client_pid}")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseTmuxClientList(out), nil
}

// parseTmuxClientList turns tmux's `-F '#{client_pid}'` output into a
// slice of TmuxClient. Split out for unit testing — the exec call in
// listTmuxClients is hard to mock without standing up a real tmux,
// but the parser is pure bytes-in / slice-out.
func parseTmuxClientList(out []byte) []TmuxClient {
	var clients []TmuxClient
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			continue // skip malformed lines rather than fail the whole query
		}
		clients = append(clients, TmuxClient{PID: pid})
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
		return bytes.Contains(b, []byte("SSH_CONNECTION="))
	case "darwin":
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		out, err := exec.CommandContext(ctx, "ps", "-E", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return false
		}
		return bytes.Contains(out, []byte("SSH_CONNECTION="))
	}
	return false
}

// nativeClipboardTool returns the argv for the OS's clipboard reader.
// Order mirrors nativeClipboardPipe in clipboard.go (Wayland before X
// on Linux). Empty slice when no tool is available.
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
