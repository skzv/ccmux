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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

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
	s.BinaryPath = filepath.Join(home, ".local", "bin", "ccmuxd")
	if _, err := os.Stat(s.BinaryPath); err == nil {
		s.BinaryInstalled = true
	}
	switch s.OS {
	case "darwin":
		probeDarwin(&s, home)
	case "linux":
		probeLinux(&s, home)
	}
	if err := exec.Command("pgrep", "-x", "ccmuxd").Run(); err == nil {
		s.Running = true
	}
	return s
}

// Install writes the service config and registers it with the OS
// service manager. Idempotent — re-running re-applies the file in case
// the binary path changed.
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
	_ = exec.Command("pkill", "-TERM", "-x", "ccmuxd").Run()
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
	_ = exec.Command("pkill", "-TERM", "-x", "ccmuxd").Run()
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

// UnitFile returns the printable systemd-user unit for users who'd
// rather install manually. Same content the linux Install() writes.
func UnitFile(binary string) string {
	return UnitFileWithPath(binary, "%h/.local/bin:/usr/local/bin:/usr/bin:/bin")
}

func UnitFileWithPath(binary, pathEnv string) string {
	return fmt.Sprintf(`[Unit]
Description=ccmux daemon (Claude Code session supervisor)
After=default.target

[Service]
ExecStart=%s
Restart=on-failure
RestartSec=3
Environment=PATH=%s

[Install]
WantedBy=default.target
`, binary, pathEnv)
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
	for _, cmd := range []string{commands.Claude, commands.Codex, commands.Antigravity, commands.Cursor, commands.Pi} {
		if cmd = strings.TrimSpace(cmd); cmd != "" {
			add(filepath.Dir(cmd))
		}
	}
	for _, p := range defaults {
		add(p)
	}
	return strings.Join(parts, ":")
}

// requireBinary fails fast if ccmuxd isn't where the service config
// would expect it. Shared by both Install paths.
func requireBinary(s Status) error {
	if !s.BinaryInstalled {
		return fmt.Errorf("ccmuxd not found at %s — run `make install` first", s.BinaryPath)
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
