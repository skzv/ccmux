// Package tmux is a thin wrapper around the tmux CLI.
// All tmux interaction in ccmux goes through here so we have a single place
// for shell-out escaping, error handling, and faking in tests.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"
)

// withStderr enriches an error from exec.Cmd.Output() with the captured
// stderr text. Output() stores stderr in ExitError.Stderr but keeps
// Error() as a bare "exit status N" — so callers matching on tmux's
// diagnostic text ("can't find session", …) never saw it and every
// failure looked the same. Wrapping with %w keeps errors.As/Is chains
// intact while making the stderr text part of the message.
func withStderr(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if msg := strings.TrimSpace(string(ee.Stderr)); msg != "" {
			return fmt.Errorf("%w (%s)", err, msg)
		}
	}
	return err
}

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
//
// WaitDelay makes the context actually bound the call. A tmux client
// hands its stdout/stderr to the server, so with a wedged server (one
// that's SIGSTOP'd, most plausibly) the pipe's write end stays open
// after the context kills the client, and Output/CombinedOutput
// waited for an EOF that only came once the server ran again —
// stalling the daemon's poll loop and every /v1/sessions request for
// as long as the server was stopped, whatever their timeouts said.
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = withLocale(os.Environ())
	cmd.WaitDelay = commandWaitDelay
	return cmd
}

// commandWaitDelay is how long a tmux call's Wait may keep reading its
// pipes after the context is done (or after tmux exits): long enough
// never to cut off a healthy call's output, short enough that a
// cancelled call returns promptly.
const commandWaitDelay = 2 * time.Second

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

// exactSession wraps a session name as a tmux target that requires an
// exact match. Without the "=" prefix tmux falls back to prefix and
// fnmatch matching, so `has-session -t c-foo` silently matches an
// existing `c-foo-app`. Every caller below targets one specific
// session by its full name, so prefix matching is never what we want.
//
// The trailing ":" makes tmux read the whole thing as the session part
// of session:window.pane. Without it a name containing "." is split —
// `-t =api.v2` means session "api", pane "v2" — so dotted session
// names (tmux allows them) could be created but never found, killed
// or attached. The same form works for target-session and
// target-pane arguments.
func exactSession(name string) string { return "=" + name + ":" }

// ExactSession is exactSession for packages that build their own tmux
// command lines (internal/tmuxchrome): the `-t` target that matches the
// session called name and nothing else. Append a window index for a
// window target ("=name:1").
func ExactSession(name string) string { return exactSession(name) }

// exactPane targets a session's active pane with an exact session
// match (capture-pane / send-keys / display-message take a
// target-pane). Same string as exactSession; kept as a separate name
// so call sites say which kind of target they pass.
func exactPane(name string) string { return exactSession(name) }

// ValidTarget reports whether name, as the session in exactSession's
// `=name:` target, reaches the session called name and nothing else.
// It is the one rule shared by everything that targets a session a
// user or a peer named: the daemon's HTTP API (badSessionName in
// cmd/ccmuxd) and the CLI's kill, rename, attach and shell --name.
//
// What tmux does with these targets (checked on tmux 3.7c):
//
//   - A leading "$" is read as a session ID even in the exact form.
//     `kill-session -t '=$1:'` kills whichever session has ID $1, and a
//     session created as "$x" can never be found by that name — "=$5:"
//     is the session whose ID is $5, not the one named "$5".
//   - A leading "%" or "@" is not: pane (%N) and window (@N) IDs are
//     only recognised in a bare target. "=%1:" finds the session named
//     "%1", or nothing — never the session holding pane %1 — so those
//     names are allowed.
//   - ":" ends the session part ("=b:1:" reaches session b), "." splits
//     a bare target into window and pane (and older tmux, e.g. 3.4,
//     rewrites it to "_" in new names), "/" and "\" are path separators
//     on the daemon's /v1/sessions/<name>/… routes, and no session can
//     hold a control character (tmux 3.7 refuses it, older versions
//     store it escaped).
//
// The empty name is not a target either.
func ValidTarget(name string) bool { return CheckTarget(name) == nil }

// ErrSessionIDTarget is CheckTarget's error for a name that is
// otherwise a plain session name but starts with "$".
var ErrSessionIDTarget = errors.New(`tmux reads a session name starting with "$" as a session ID, so it would act on a different session`)

var (
	errEmptyTarget    = errors.New("session name is empty")
	errTargetSpecials = errors.New(`ccmux can't target a session whose name contains /, \, :, . or a control character`)
)

// CheckTarget is ValidTarget with the reason it fails: nil for a valid
// target, ErrSessionIDTarget for a name whose only problem is a leading
// "$", and a plain error otherwise. Callers whose argument may also be
// a project name or a path (`ccmux kill`, `ccmux attach`) refuse
// ErrSessionIDTarget outright but let any other invalid name fall
// through to their project mapping.
func CheckTarget(name string) error {
	switch {
	case name == "":
		return errEmptyTarget
	case strings.ContainsAny(name, `/\:.`) || strings.ContainsFunc(name, unicode.IsControl):
		return errTargetSpecials
	case strings.HasPrefix(name, "$"):
		return ErrSessionIDTarget
	}
	return nil
}

// Session is the static metadata about a tmux session.
type Session struct {
	Agent string // optional explicit agent for a resumed conversation
	// Spinner is the agent (its ID) the daemon has seen announce its turns
	// with a working-spinner title in this session — the session's
	// @ccmux_spinner option, set by SetSessionSpinner — or "" for none.
	Spinner    string
	Name       string    // tmux session name, e.g. "c-foo"
	Created    time.Time // tmux's create timestamp
	LastAttach time.Time // tmux's last activity timestamp
	Path       string    // session's default working directory
	Attached   bool      // whether any client is currently attached
	Windows    int
}

// Has reports whether a session by the given name exists on the default tmux server.
func Has(ctx context.Context, name string) (bool, error) {
	cmd := command(ctx, "tmux", "has-session", "-t", exactSession(name))
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
// session_path is LAST so a tab inside a directory path can't shift the
// later columns. parseList uses SplitN with the field count, letting the
// final field absorb any embedded tabs. The other fields are
// tmux-generated numbers + the session name, which can't contain tabs.
const listFormat = "#{session_name}\t#{session_created}\t#{session_activity}\t#{session_attached}\t#{session_windows}\t#{session_path}"

// List returns every session on the default tmux server.
// Returns an empty slice if the tmux server is not running.
func List(ctx context.Context) ([]Session, error) {
	cmd := command(ctx, "tmux", "list-sessions", "-F", listFormat)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && noServerRunning(string(exitErr.Stderr)) {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %w", withStderr(err))
	}
	sessions := parseList(out)
	tags, err := command(ctx, "tmux", "list-sessions", "-F", sessionTagsFormat).Output()
	if err != nil {
		return nil, fmt.Errorf("read tmux session agents: %w", withStderr(err))
	}
	applySessionTags(sessions, tags)
	return sessions, nil
}

// sessionTagsFormat reads the user options ccmux keeps on each session
// (see applySessionTags): what it runs (@ccmux_agent) and the agent seen
// with a working-spinner title there (@ccmux_spinner).
const sessionTagsFormat = "#{session_name}\t#{" + agentOption + "}\t#{" + spinnerOption + "}"

// noServerRunning reports whether a failed tmux command's stderr means
// there is simply no tmux server: "no server running on <socket>" (a
// socket file with nothing listening) or "error connecting to <socket>
// (No such file or directory)" (no socket at all). tmux exits 1 for
// every failure, so the exit code alone can't tell these apart from a
// socket it may not open ("Permission denied") or a server that failed
// mid-command — and callers that read "no sessions" as "every session
// ended" (the daemon's cleanup pass) must not see those as empty.
func noServerRunning(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "no server running") ||
		(strings.Contains(s, "error connecting to") && strings.Contains(s, "no such file or directory"))
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
		// SplitN with the exact field count so the final field
		// (session_path) captures any tab a directory path might
		// contain instead of spilling into a phantom 7th column.
		parts := strings.SplitN(line, "\t", 6)
		if len(parts) < 6 {
			continue
		}
		s := Session{
			Name:     parts[0],
			Created:  unixSecs(parts[1]),
			Path:     parts[5],
			Attached: parts[3] != "0",
			Windows:  atoi(parts[4]),
		}
		s.LastAttach = unixSecs(parts[2])
		sessions = append(sessions, s)
	}
	return sessions
}

// New creates a new detached session named `name`, starting `cmdline` in directory `dir`.
// If cmdline is empty, tmux's default shell is used.
func New(ctx context.Context, name, dir, cmdline string) error {
	return NewWithAgent(ctx, name, dir, cmdline, "")
}

// NewWithAgent is New plus the session's @ccmux_agent tag (see
// SetSessionAgent), set by the same tmux invocation —
// `new-session … ; set-option …` — so the session never exists
// untagged. Tagging with a second call left a window in which the
// daemon's poll tick classified the new session by its project's agent
// (Claude for a bare shell, whose prompt then read as a crashed Claude).
// An empty agentTag is plain New.
func NewWithAgent(ctx context.Context, name, dir, cmdline, agentTag string) error {
	args := []string{"new-session", "-d", "-s", name}
	if dir != "" {
		args = append(args, "-c", escapeFormat(dir))
	}
	if cmdline != "" {
		args = append(args, cmdline)
	}
	if agentTag != "" {
		// A lone ";" argument separates tmux commands.
		args = append(args, ";", "set-option", "-t", exactSession(name), agentOption, agentTag)
	}
	cmd := command(ctx, "tmux", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// escapeFormat protects a literal string that tmux format-expands, such
// as new-session's -c start directory: `##` is tmux's escape for `#`.
// Unescaped, a project directory "with#hash" started its session in
// "with<hostname>ash" (#h), which doesn't exist, so the agent ran in
// $HOME — and "#(…)" in a directory name would run a command.
func escapeFormat(s string) string { return strings.ReplaceAll(s, "#", "##") }

// Kill terminates the named session.
//
// A name starting with "$" is refused (ErrSessionIDTarget): tmux would
// read it as a session ID and kill whichever session has that ID. Every
// caller that takes a name from a user checks ValidTarget first; this
// also covers a name read back from tmux's own session list — a session
// created outside ccmux as "$5" — which no target can reach by name.
func Kill(ctx context.Context, name string) error {
	if strings.HasPrefix(name, "$") {
		return fmt.Errorf("tmux kill-session %q: %w", name, ErrSessionIDTarget)
	}
	cmd := command(ctx, "tmux", "kill-session", "-t", exactSession(name))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux kill-session: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Rename renames a session. As with Kill, an old name starting with "$"
// is refused (ErrSessionIDTarget): it would rename the session with
// that ID.
func Rename(ctx context.Context, oldName, newName string) error {
	if strings.HasPrefix(oldName, "$") {
		return fmt.Errorf("tmux rename-session %q: %w", oldName, ErrSessionIDTarget)
	}
	// "--" so a new name starting with "-" is a name, not a flag.
	cmd := command(ctx, "tmux", "rename-session", "-t", exactSession(oldName), "--", newName)
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
		return "", fmt.Errorf("tmux capture-pane: %w", withStderr(err))
	}
	return string(out), nil
}

// PaneTitle returns the current value of `#{pane_title}` for the named
// session's active pane. This is the title the *inner* program (the
// agent CLI) set via the OSC 2 escape sequence — distinct from the
// outer tmux/terminal title controlled by `set-titles-string`.
//
// Agent CLIs broadcast their state here far more reliably than they
// do in the pane body: braille spinner chars while working, strings
// like "Action Required" when blocked. Reading it is a small `tmux
// display-message -p` shell-out — orders of magnitude smaller than
// capture-pane — and feeds straight into the classifier as a high-
// priority signal alongside the body.
//
// Returns "" with no error on a missing session (consistent with
// "title not available") so callers can pass the result straight
// through without special-casing absent sessions.
func PaneTitle(ctx context.Context, name string) (string, error) {
	args := []string{"display-message", "-p", "-t", exactPane(name), "#{pane_title}"}
	cmd := command(ctx, "tmux", args...)
	out, err := cmd.Output()
	if err != nil {
		// `display-message` errors if the session is gone or the pane
		// id is malformed. Treat both as "no title" — the classifier
		// will fall back to body-only detection, identical behavior
		// to before this signal existed.
		return "", nil
	}
	// display-message terminates with a newline; trim once.
	s := string(out)
	if n := len(s); n > 0 && s[n-1] == '\n' {
		s = s[:n-1]
	}
	return s, nil
}

// SendKeys sends a literal key sequence to the named session.
func SendKeys(ctx context.Context, name, keys string) error {
	// "--" ends tmux's option parsing: without it text such as
	// "- fix the bug", "-1" or "--help" failed as an unknown flag, and
	// "-R" silently reset the pane instead of being typed.
	cmd := command(ctx, "tmux", "send-keys", "-t", exactPane(name), "--", keys)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RingBell sends BEL directly to every attached tmux client for the session.
// Do not use send-keys for this: BEL is Ctrl-G, and interactive agents such
// as Claude Code and Codex bind Ctrl-G to "open prompt in external editor".
func RingBell(ctx context.Context, name string) error {
	cmd := command(ctx, "tmux", "list-clients", "-t", exactSession(name), "-F", "#{client_tty}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux list-clients: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	var errs []string
	for _, tty := range clientTTYs(out) {
		if err := writeBellToTTY(tty); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", tty, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("tmux ring bell: %s", strings.Join(errs, "; "))
	}
	return nil
}

func clientTTYs(raw []byte) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, line := range strings.Split(string(raw), "\n") {
		tty := strings.TrimSpace(line)
		if tty == "" || seen[tty] || !strings.HasPrefix(tty, "/dev/") {
			continue
		}
		seen[tty] = true
		out = append(out, tty)
	}
	return out
}

// SendText types `text` into the named session's pane verbatim. The
// `-l` flag disables key-name lookup, so user-supplied text containing
// a key name (e.g. an initial prompt of "Press Enter to begin") is
// typed as characters instead of being interpreted as the Enter key.
// Call SendKeys(ctx, name, "Enter") separately to submit.
func SendText(ctx context.Context, name, text string) error {
	cmd := command(ctx, "tmux", "send-keys", "-t", exactPane(name), "-l", "--", text)
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
//
// The target is exact-matched: a bare `-t c-foo` falls back to prefix
// matching, so attaching to a just-killed c-foo would land in c-foo-app.
func AttachArgs(name string, detachOthers bool) []string {
	if detachOthers {
		return []string{"attach-session", "-d", "-t", exactSession(name)}
	}
	return []string{"attach-session", "-t", exactSession(name)}
}

// AttachCmd builds the *exec.Cmd that, when passed to tea.ExecProcess,
// foregrounds a tmux attach. Centralizes the "tmux + AttachArgs" call
// so the TUI doesn't shell out to tmux directly (per CLAUDE.md's "all
// tmux operations go through internal/tmux" rule).
func AttachCmd(name string, detachOthers bool) *exec.Cmd {
	// nocontext: a foreground attach lives as long as the user stays attached.
	return exec.Command("tmux", AttachArgs(name, detachOthers)...)
}

// SwitchClientCmd builds the *exec.Cmd that switches the current tmux
// client to a different session. Used by the nested-tmux case: when
// ccmux runs inside a tmux session itself, attach-session is refused
// — switch-client is the correct verb.
func SwitchClientCmd(name string) *exec.Cmd {
	// nocontext: handed to tea.ExecProcess like AttachCmd.
	return exec.Command("tmux", "switch-client", "-t", exactSession(name))
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
//
// Rewriting is lossy — `my.app` and `my_app` both sanitize to `my_app`,
// and every all-non-ASCII name of the same byte length (`日本`, `中文`)
// to the same run of underscores — and two projects sharing a session
// name meant attaching to the wrong project's agent, "duplicate
// session" when opening the second one, and the daemon handing back
// the other project's session. So when sanitizing changed anything, a
// short stable tag of the original name is appended (`my.app` →
// `c-my_app-ipltv`). Names already in the safe alphabet map exactly as
// before, so their existing sessions keep attaching.
func SessionNameForPath(path string) string {
	return SessionNameForBase(lastSegment(path))
}

// ValidSessionName reports whether name is a session name ccmux accepts
// from the user (`ccmux rename`, the TUI rename form): letters, digits,
// "_" and "-" — the alphabet SessionNameForPath produces — not starting
// with "-" (it would read as a flag in `ccmux kill <name>`). No "." —
// tmux versions differ on whether they keep it or rewrite it to "_", so
// a dotted rename could leave the session under a name nobody asked
// for, and the daemon rejects dotted names for the same reason — and no
// ":" or "/", which a -t target would parse.
func ValidSessionName(name string) bool {
	if name == "" || name[0] == '-' {
		return false
	}
	return sanitizeSessionName(name) == name
}

// SessionNameForBase is SessionNameForPath for a directory name that
// has already been split off its path (project.Project.Name), applied
// to the whole string.
func SessionNameForBase(base string) string {
	name := sanitizeSessionName(base)
	if name != base {
		name += "-" + sessionNameTag(base)
	}
	return "c-" + name
}

// sessionNameTag is a five-letter digest of name (FNV-1a folded into
// base 26). Letters only, so it can never look like the numeric `-2`,
// `-3` suffix ccmux gives a project's additional sessions.
func sessionNameTag(name string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	v := h.Sum32()
	var tag [5]byte
	for i := range tag {
		tag[i] = 'a' + byte(v%26)
		v /= 26
	}
	return string(tag[:])
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

// ShellAgentTag is the @ccmux_agent value for a session running a plain
// shell rather than a coding agent. Such sessions are never classified
// with an agent's rules.
const ShellAgentTag = "shell"

// agentOption is the tmux user option that tags a session with what it
// runs (read back by List as Session.Agent).
const agentOption = "@ccmux_agent"

// SetSessionAgent pins a resumed conversation's agent without changing the
// workspace's default agent or other sessions running in that workspace.
// A session ccmux creates itself should get its tag from NewWithAgent.
func SetSessionAgent(ctx context.Context, name, id string) error {
	if err := setSessionOption(ctx, name, agentOption, id); err != nil {
		return fmt.Errorf("set session agent: %w", err)
	}
	return nil
}

// spinnerOption is the tmux user option on which the daemon records the
// agent it has seen show a working-spinner title in a session (read
// back by List as Session.Spinner).
const spinnerOption = "@ccmux_spinner"

// SetSessionSpinner records on session name that agent id announces its
// turns there with a working-spinner title. The daemon learns that by
// watching, and relies on it to tell the agent's turns from the user
// typing into it; kept in the session itself, it outlives a daemon
// restart instead of being learned again (with a notification for
// whatever was typed first).
func SetSessionSpinner(ctx context.Context, name, id string) error {
	if err := setSessionOption(ctx, name, spinnerOption, id); err != nil {
		return fmt.Errorf("set session spinner: %w", err)
	}
	return nil
}

// setSessionOption sets the user option (an "@…" name) on the session
// called name.
func setSessionOption(ctx context.Context, name, option, value string) error {
	if out, err := command(ctx, "tmux", "set-option", "-t", exactPane(name), option, value).CombinedOutput(); err != nil {
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// applySessionTags fills in each session's Agent and Spinner from raw,
// the output of `list-sessions -F sessionTagsFormat`.
func applySessionTags(sessions []Session, raw []byte) {
	type tags struct{ agent, spinner string }
	byName := make(map[string]tags, len(sessions))
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		tg := tags{agent: parts[1]}
		if len(parts) == 3 {
			tg.spinner = parts[2]
		}
		byName[parts[0]] = tg
	}
	for i := range sessions {
		tg := byName[sessions[i].Name]
		sessions[i].Agent, sessions[i].Spinner = tg.agent, tg.spinner
	}
}
