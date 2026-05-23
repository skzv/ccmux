// Package tmux is a thin wrapper around the tmux CLI.
// All tmux interaction in ccmux goes through here so we have a single place
// for shell-out escaping, error handling, and faking in tests.
package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// autoSeq backs AutoSessionName's collision-free suffix.
var autoSeq atomic.Int64

// AutoSessionName mints a process-unique tmux session name for a
// session the user did not name (e.g. the "new bare session" form
// left the name blank). A wall-clock timestamp alone can repeat —
// two calls within the same millisecond, or even the same nanosecond
// on a coarse clock — so an atomic counter is appended to guarantee
// every call within the process returns a distinct name. `prefix` is
// the leading segment, conventionally "c-shell".
func AutoSessionName(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), autoSeq.Add(1))
}

// command builds an *exec.Cmd for a tmux invocation with a UTF-8 locale
// forced. Without this, when ccmuxd runs under launchd/systemd no LANG or
// LC_* vars are inherited and tmux falls back to the C locale — in which
// case `-F` output strips tabs (and other non-printable bytes) and replaces
// them with `_`, breaking our parser. Setting LC_ALL=C.UTF-8 keeps tmux's
// output bytes intact regardless of the launcher's environment.
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = withLocale(os.Environ())
	return cmd
}

// withLocale returns env with LC_ALL=C.UTF-8 appended iff none of
// LC_ALL / LC_CTYPE / LANG are already set. Pulled out of command() so
// the locale-decision logic is unit-testable without spawning a process.
func withLocale(env []string) []string {
	for _, e := range env {
		if strings.HasPrefix(e, "LC_ALL=") || strings.HasPrefix(e, "LC_CTYPE=") || strings.HasPrefix(e, "LANG=") {
			return env
		}
	}
	return append(env, "LC_ALL=C.UTF-8")
}

// exactSession wraps a session name as a tmux target-session that
// requires an exact match. Without the "=" prefix tmux falls back to
// prefix and fnmatch matching, so `has-session -t c-foo` silently
// matches an existing `c-foo-app`. Every caller below targets one
// specific session by its full name, so prefix matching is never
// what we want.
func exactSession(name string) string { return "=" + name }

// exactPane targets a session's active pane with an exact session
// match. capture-pane / send-keys / resize-window take a target-PANE,
// not a target-session, and a bare "=name" is rejected for those —
// the trailing ":" makes tmux parse it as session:window.pane with
// the session component exact-matched.
func exactPane(name string) string { return "=" + name + ":" }

// Session is the static metadata about a tmux session.
type Session struct {
	Name       string    // tmux session name, e.g. "c-foo"
	Created    time.Time // tmux's create timestamp
	LastAttach time.Time // tmux's last activity timestamp
	Path       string    // session's default working directory
	Attached   bool      // whether any client is currently attached
	Windows    int
}

// Has reports whether a session by the given name exists on the default tmux server.
func Has(ctx context.Context, name string) (bool, error) {
	cmd := exec.CommandContext(ctx, "tmux", "has-session", "-t", exactSession(name))
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// tmux exits 1 when the session doesn't exist; that's not a "real" error.
			if exitErr.ExitCode() == 1 {
				return false, nil
			}
		}
		return false, fmt.Errorf("tmux has-session: %w", err)
	}
	return true, nil
}

// listFormat is the tmux -F format used by List. Exported as a constant
// so tests can verify the parser stays aligned with the format string.
const listFormat = "#{session_name}\t#{session_created}\t#{session_activity}\t#{session_path}\t#{session_attached}\t#{session_windows}"

// List returns every session on the default tmux server.
// Returns an empty slice if the tmux server is not running.
func List(ctx context.Context) ([]Session, error) {
	cmd := command(ctx, "tmux", "list-sessions", "-F", listFormat)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// tmux prints "no server running on /tmp/tmux-…" to stderr and exits 1.
			// Treat that as "no sessions" rather than an error.
			if exitErr.ExitCode() == 1 {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("tmux list-sessions: %w", err)
	}
	return parseList(out), nil
}

// parseList turns raw `tmux list-sessions -F listFormat` output into
// Session values. Split out so tests can exercise the parser directly
// without needing a tmux server.
func parseList(out []byte) []Session {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	sessions := make([]Session, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 6 {
			continue
		}
		s := Session{
			Name:     parts[0],
			Created:  unixSecs(parts[1]),
			Path:     parts[3],
			Attached: parts[4] != "0",
			Windows:  atoi(parts[5]),
		}
		s.LastAttach = unixSecs(parts[2])
		sessions = append(sessions, s)
	}
	return sessions
}

// New creates a new detached session named `name`, starting `cmdline` in directory `dir`.
// If cmdline is empty, tmux's default shell is used.
func New(ctx context.Context, name, dir, cmdline string) error {
	args := []string{"new-session", "-d", "-s", name}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	if cmdline != "" {
		args = append(args, cmdline)
	}
	cmd := exec.CommandContext(ctx, "tmux", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Kill terminates the named session.
func Kill(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, "tmux", "kill-session", "-t", exactSession(name))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux kill-session: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Rename renames a session.
func Rename(ctx context.Context, oldName, newName string) error {
	cmd := exec.CommandContext(ctx, "tmux", "rename-session", "-t", exactSession(oldName), newName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux rename-session: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CapturePane returns the visible content of the named session's active pane.
// Used both for the live-preview pane in the TUI and for "needs input" detection
// by ccmuxd.
func CapturePane(ctx context.Context, name string, lines int) (string, error) {
	args := []string{"capture-pane", "-p", "-t", exactPane(name)}
	if lines > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", lines))
	}
	cmd := command(ctx, "tmux", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}
	return string(out), nil
}

// SendKeys sends a literal key sequence to the named session.
// Use this to inject a BEL byte for notification triggers (`SendKeys(ctx, name, "\a")`).
func SendKeys(ctx context.Context, name, keys string) error {
	cmd := exec.CommandContext(ctx, "tmux", "send-keys", "-t", exactPane(name), keys)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SendText types `text` into the named session's pane verbatim. The
// `-l` flag disables key-name lookup, so user-supplied text containing
// a key name (e.g. an initial prompt of "Press Enter to begin") is
// typed as characters instead of being interpreted as the Enter key.
// Call SendKeys(ctx, name, "Enter") separately to submit.
func SendText(ctx context.Context, name, text string) error {
	cmd := exec.CommandContext(ctx, "tmux", "send-keys", "-t", exactPane(name), "-l", text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys -l: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// AttachArgs returns the tmux argument vector (everything after the
// `tmux` binary itself) for attaching to `name`. The single source of
// truth for the attach command shape — local exec, remote ssh, and the
// foreground Attach() below all build on this so the -d decision can't
// drift between paths.
//
// detachOthers=true appends -d ("exclusive" mode — kick other clients);
// false omits it ("mirror" mode — other clients stay attached).
func AttachArgs(name string, detachOthers bool) []string {
	if detachOthers {
		return []string{"attach-session", "-d", "-t", name}
	}
	return []string{"attach-session", "-t", name}
}

// Attach replaces the current process with `tmux attach -t name`.
// This must be called from the foreground of a terminal — typically the TUI
// suspends itself first, then re-execs into tmux. After tmux detaches, the
// caller resumes.
//
// detachOthers controls the -d flag:
//   - false (mirror mode) — other clients stay attached; the session is
//     mirrored across every device viewing it.
//   - true (exclusive mode) — attaching kicks every other client off,
//     and the session resizes cleanly to this terminal.
//
// On success this function does not return (syscall.Exec replaces the process).
func Attach(name string, detachOthers bool) error {
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not on PATH: %w", err)
	}
	argv := append([]string{"tmux"}, AttachArgs(name, detachOthers)...)
	return syscall.Exec(tmuxBin, argv, os.Environ())
}

// SessionNameForPath converts a filesystem path to ccmux's tmux session-naming
// convention: `c-<basename>` with any character that's special to tmux's
// `-t` target parser or to a POSIX shell replaced by `_`.
//
// The historical version only swapped `.` → `_`, which covered the common
// dotted-name case but missed `:` (tmux's session/window separator),
// `\n` / `\x00` (control bytes that would corrupt `-t` arg parsing), and
// shell metacharacters that could matter if the name ever ends up in a
// quoted command string. We use an allowlist — keep `[a-zA-Z0-9_-]`,
// rewrite everything else to `_` — because the set of "safe enough"
// characters across tmux + zsh + ssh quoting is small, and an allowlist
// is the only way to guarantee a future caller that we haven't shipped
// is safe by default. Matches the existing `cc()` zsh function output
// for all names that were already safe.
func SessionNameForPath(path string) string {
	base := lastSegment(path)
	return "c-" + sanitizeSessionName(base)
}

// sanitizeSessionName rewrites `name` into the tmux-safe alphabet.
// Exported package-private so the fuzz target can re-derive the same
// transform.
func sanitizeSessionName(name string) string {
	if name == "" {
		return ""
	}
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		b := name[i]
		switch {
		case b >= 'a' && b <= 'z',
			b >= 'A' && b <= 'Z',
			b >= '0' && b <= '9',
			b == '_', b == '-':
			out = append(out, b)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

func lastSegment(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func unixSecs(s string) time.Time {
	n := atoi(s)
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(int64(n), 0)
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}
