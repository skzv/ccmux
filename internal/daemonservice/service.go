// Package daemonservice manages ccmuxd as a long-lived OS service so it
// survives logout / reboot.
//
// Two backends, dispatched at runtime by GOOS:
//
//   - macOS: launchd via ~/Library/LaunchAgents/dev.ccmux.daemon.plist
//     (see darwin.go).
//   - Linux: systemd-user via ~/.config/systemd/user/ccmuxd.service
//     (see linux.go).
//
// Other GOOS values get a "not supported" error from Install; Status
// still works (just reports the running process).
package daemonservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/agent"
)

// Label is the service identifier — used as the launchd Label and as
// the systemd unit basename.
const Label = "dev.ccmux.daemon"

// Status describes whether ccmuxd is registered and running. The field
// names are platform-neutral; both backends populate them with their
// equivalents (plist file on mac, .service file on linux).
type Status struct {
	OS              string // "darwin" | "linux" | other
	ServicePath     string // absolute path to the service config file
	ServiceExists   bool   // file present on disk
	ServiceEnabled  bool   // registered with the OS service manager
	Running         bool   // ccmuxd process actually alive
	BinaryInstalled bool   // ccmuxd binary present where the service config expects it
	BinaryPath      string // path the service config will exec
}

// Probe returns the current state. Safe to call any time. Falls back
// to "process alive?" only for unsupported platforms.
func Probe() Status {
	s := Status{OS: runtime.GOOS}
	home, err := os.UserHomeDir()
	if err != nil {
		return s
	}
	s.BinaryPath, s.BinaryInstalled = resolveCcmuxdBinary(home)
	switch s.OS {
	case "darwin":
		probeDarwin(&s, home)
	case "linux":
		probeLinux(&s, home)
	}
	if err := runCmd("pgrep", "-U", uid(), "-x", "ccmuxd"); err == nil {
		s.Running = true
	}
	return s
}

// Install writes the service config and registers it with the OS
// service manager. Idempotent — re-running rewrites the file and
// restarts the daemon, picking up a binary path that moved or an
// agent-home override (see passthroughEnvNames) set since the last run.
func Install() (Status, error) {
	switch runtime.GOOS {
	case "darwin":
		return installDarwin()
	case "linux":
		return installLinux()
	}
	return Probe(), fmt.Errorf("auto-install not supported on %s", runtime.GOOS)
}

// Uninstall reverses Install: disables the service, kills the process,
// removes the service config file.
func Uninstall() (Status, error) {
	switch runtime.GOOS {
	case "darwin":
		return uninstallDarwin()
	case "linux":
		return uninstallLinux()
	}
	// Best-effort kill on other platforms.
	_ = runCmd("pkill", "-TERM", "-U", uid(), "-x", "ccmuxd")
	return Probe(), nil
}

// Restart bounces the running daemon so a newly-installed binary takes
// effect. On darwin uses `launchctl kickstart -k`; on linux uses
// `systemctl --user restart`. Falls back to a SIGTERM + relaunch via
// the existing service config when neither is available. Returns the
// post-restart Probe() so the caller can confirm the daemon is back.
func Restart() (Status, error) {
	switch runtime.GOOS {
	case "darwin":
		return restartDarwin()
	case "linux":
		return restartLinux()
	}
	_ = runCmd("pkill", "-TERM", "-U", uid(), "-x", "ccmuxd")
	return Probe(), fmt.Errorf("restart not supported on %s", runtime.GOOS)
}

// ServicePathOrEmpty exposes the resolved path (plist or unit) so the
// main `ccmux uninstall` flow can preview/remove it without
// duplicating path resolution. Returns "" on unsupported platforms.
func ServicePathOrEmpty() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user", "ccmuxd.service")
	}
	return ""
}

// passthroughEnvNames is the one list of environment variables Install
// copies from the installing shell into the launchd plist / systemd
// unit. A service manager starts ccmuxd with almost nothing in its
// environment (we write HOME and PATH ourselves) and never reads the
// user's shell profile, so an override exported there would reach the
// TUI but not the daemon: its usage walker and conversation listing
// would quietly read the default location instead. A tmux server the
// daemon ends up starting inherits that same bare environment, and so
// do the agents launched inside it.
//
// Only directory overrides belong here — each one is a variable ccmux
// itself reads to find an agent's files. Never add credentials
// (ANTHROPIC_API_KEY, OpenRouter keys, …): the plist and unit are
// written world-readable (0644).
var passthroughEnvNames = []string{
	// Claude Code's config root (default ~/.claude); resolved by
	// internal/claudeconfig and the setup wizard's MCP registration.
	"CLAUDE_CONFIG_DIR",
	// Codex's home (default ~/.codex); resolved by internal/codexpath
	// for its config, and for the sessions/ transcripts the daemon's
	// usage walker and conversation listing read.
	"CODEX_HOME",
	// Cursor's config root (default ~/.cursor); resolved by
	// internal/cursorconfig.
	"CURSOR_HOME",
	// The Antigravity CLI's root (default ~/.gemini/antigravity-cli);
	// resolved by internal/antigravityconfig.
	"ANTIGRAVITY_HOME",
}

// EnvVar is one NAME=value pair written into the service definition.
type EnvVar struct {
	Name  string
	Value string
}

// PassthroughEnvNames returns the variables Install carries over from
// the installing shell when they are set, for help text and docs.
func PassthroughEnvNames() []string {
	return append([]string(nil), passthroughEnvNames...)
}

// PassthroughEnv returns the agent-home overrides set (and non-empty) in
// this process's environment, in a fixed order — exactly the extra
// variables Install writes into the service definition.
func PassthroughEnv() []EnvVar {
	return passthroughEnv(os.Getenv)
}

func passthroughEnv(getenv func(string) string) []EnvVar {
	var out []EnvVar
	for _, name := range passthroughEnvNames {
		if v := getenv(name); v != "" {
			out = append(out, EnvVar{Name: name, Value: v})
		}
	}
	return out
}

// installer is the write-and-register half of Install with its
// outside-world inputs injected: the home dir the service file lives
// under, where the agent-home overrides are read from, and the
// service-manager runner. Install builds one over the real process;
// tests build one over a temp dir and a fake runner, so they never
// touch ~/Library/LaunchAgents, launchctl or systemctl.
type installer struct {
	home     string
	binary   string
	commands agent.Commands
	getenv   func(string) string
	run      func(name string, args ...string) ([]byte, error) // combined output
}

func newInstaller(home, binary string, commands agent.Commands) installer {
	return installer{home: home, binary: binary, commands: commands, getenv: os.Getenv, run: combinedCmd}
}

// UnitFile returns the printable systemd-user unit for users who'd
// rather install manually: the same unit the linux Install() writes,
// including the agent-home overrides set in this process's environment.
func UnitFile(binary string) string {
	return unitFile(binary, "%h/.local/bin:/usr/local/bin:/usr/bin:/bin", PassthroughEnv())
}

// unitFile renders the systemd-user unit. pathEnv is written verbatim
// (UnitFile relies on the %h specifier in it); every env value is
// escaped by systemdEnvAssignment.
func unitFile(binary, pathEnv string, env []EnvVar) string {
	var b strings.Builder
	fmt.Fprintf(&b, `[Unit]
Description=ccmux daemon (Claude Code session supervisor)
After=default.target

[Service]
ExecStart=%s
Restart=on-failure
RestartSec=3
Environment=PATH=%s
`, binary, pathEnv)
	for _, e := range env {
		fmt.Fprintf(&b, "Environment=%s\n", systemdEnvAssignment(e.Name, e.Value))
	}
	b.WriteString(`
[Install]
WantedBy=default.target
`)
	return b.String()
}

// systemdEnvAssignment renders NAME=value for an Environment= line so
// systemd hands the daemon value byte for byte. The whole assignment is
// double-quoted, which keeps spaces and '=' inside one word. Inside the
// quotes systemd applies C-style unescaping, so backslash and double
// quote are backslash-escaped and control characters become \xNN.
// Specifiers are expanded after unquoting, so '%' is doubled to "%%".
// '$' needs nothing: Environment= does no variable expansion.
func systemdEnvAssignment(name, value string) string {
	s := name + "=" + value
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' || c == '"':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '%':
			b.WriteString("%%")
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func managedPath(home string, commands agent.Commands, defaults ...string) string {
	parts := []string{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		for _, existing := range parts {
			if existing == p {
				return
			}
		}
		parts = append(parts, p)
	}
	add(filepath.Join(home, ".local", "bin"))
	for _, cmd := range []string{commands.Claude, commands.Codex, commands.Antigravity, commands.Cursor, commands.Pi, commands.Grok, commands.Gemini} {
		if cmd = strings.TrimSpace(cmd); cmd != "" {
			add(filepath.Dir(cmd))
		}
	}
	for _, p := range defaults {
		add(p)
	}
	return strings.Join(parts, ":")
}

// resolveCcmuxdBinary finds the ccmuxd that pairs with the running
// ccmux binary and reports whether it actually exists on disk. ccmuxd
// is installed next to ccmux in every supported layout — Homebrew
// (/opt/homebrew/bin), `make install` (~/.local/bin), and a source
// build (bin/) — so we look beside the running binary first, then on
// PATH, and only fall back to the legacy ~/.local/bin default when
// nothing is found (so a "not installed" message still names a sane
// path). The returned path is what the launchd/systemd service execs.
func resolveCcmuxdBinary(home string) (path string, installed bool) {
	exe, _ := os.Executable()
	return resolveCcmuxdFrom(exe, home, fileExists, exec.LookPath)
}

// CcmuxdBinary resolves the ccmuxd that pairs with the running ccmux
// and reports whether it exists on disk. Exported so `ccmux daemon
// start` and `ccmux daemon unit` resolve the binary the same way the
// installed launchd/systemd service does — instead of each rolling its
// own (previously ~/.local/bin-hardcoded) lookup.
func CcmuxdBinary() (path string, installed bool) {
	home, _ := os.UserHomeDir()
	return resolveCcmuxdBinary(home)
}

// resolveCcmuxdFrom is the testable core of resolveCcmuxdBinary: the
// running ccmux path, the home dir, and the filesystem/PATH probes are
// all injectable.
func resolveCcmuxdFrom(exe, home string, exists func(string) bool, lookPath func(string) (string, error)) (string, bool) {
	for _, dir := range candidateBinDirs(exe) {
		cand := filepath.Join(dir, "ccmuxd")
		if exists(cand) {
			return cand, true
		}
	}
	if p, err := lookPath("ccmuxd"); err == nil && p != "" {
		return p, true
	}
	return filepath.Join(home, ".local", "bin", "ccmuxd"), false
}

// candidateBinDirs lists the directories to search for a sibling
// ccmuxd: the directory of ccmux as it was invoked (kept first because
// it's the stable path — /opt/homebrew/bin is a symlink farm that
// survives `brew upgrade`, whereas the resolved Cellar path is
// version-pinned), then the directory after resolving symlinks. An
// empty exe yields no directories.
func candidateBinDirs(exe string) []string {
	if exe == "" {
		return nil
	}
	dirs := []string{filepath.Dir(exe)}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		if rd := filepath.Dir(real); rd != dirs[0] {
			dirs = append(dirs, rd)
		}
	}
	return dirs
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// requireBinary fails fast if ccmuxd isn't where the service config
// would expect it. Shared by both Install paths.
func requireBinary(s Status) error {
	if !s.BinaryInstalled {
		return fmt.Errorf("ccmuxd binary not found next to ccmux or on PATH — reinstall ccmux (or run `make install` from a source checkout)")
	}
	return nil
}

// ensureStateDirs creates the ~/.local/state/ccmux directory used by
// both backends for stdout/stderr logs. Returns the absolute path.
func ensureStateDirs(home string) (string, error) {
	dir := filepath.Join(home, ".local", "state", "ccmux")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// removePathQuiet wraps os.Remove with a "missing is fine" allowance,
// used by both Uninstall flows when sweeping config files.
func removePathQuiet(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// commandTimeout bounds each service-manager call (launchctl,
// systemctl, pgrep, pkill) so a wedged one can't hang
// `ccmux daemon install` or the TUI's status probe forever.
const commandTimeout = 30 * time.Second

func runCmd(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Run()
}

func outputCmd(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func combinedCmd(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
