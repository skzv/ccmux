// Package daemonservice manages ccmuxd as a long-lived OS service so it
// survives logout / reboot. macOS implementation uses launchd; Linux
// callers get a printable systemd-user unit suggestion but we don't
// auto-install on Linux yet.
//
// The launchd plist lives at:
//   ~/Library/LaunchAgents/dev.ccmux.daemon.plist
// and runs $HOME/.local/bin/ccmuxd. Stdout/stderr go to
//   ~/.local/state/ccmux/ccmuxd.{stdout,stderr}.log
// so we can `tail -f` either when something's odd.
package daemonservice

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
)

// Label is the launchd / systemd identifier. Used as the plist Label
// and as the filename prefix.
const Label = "dev.ccmux.daemon"

// Status describes whether the service is registered and running.
type Status struct {
	OS              string // "darwin" | "linux" | other
	PlistPath       string // (darwin) path the plist would live at
	PlistExists     bool
	Loaded          bool   // (darwin) launchctl print-disabled / print
	Running         bool   // process actually alive (best-effort)
	BinaryInstalled bool   // ccmuxd in expected location
	BinaryPath      string
}

// Probe returns the current state. Safe to call any time.
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
	if s.OS == "darwin" {
		s.PlistPath = filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
		if _, err := os.Stat(s.PlistPath); err == nil {
			s.PlistExists = true
		}
		if out, err := exec.Command("launchctl", "list", Label).Output(); err == nil {
			// `launchctl list <label>` prints a plist-style dict when
			// loaded; exits non-zero when not loaded.
			s.Loaded = len(out) > 0
		}
	}
	// pgrep is universal-enough across darwin and linux.
	if err := exec.Command("pgrep", "-x", "ccmuxd").Run(); err == nil {
		s.Running = true
	}
	return s
}

// Install writes the plist (or returns a printable unit on Linux) and
// loads it via launchctl. Idempotent: re-running with the plist already
// loaded re-loads it after a stop, which is fine and refreshes the
// binary path in case the user moved it.
func Install() (Status, error) {
	switch runtime.GOOS {
	case "darwin":
		return installDarwin()
	case "linux":
		return Probe(), fmt.Errorf("auto-install not implemented on Linux; see `ccmux daemon unit`")
	}
	return Probe(), fmt.Errorf("auto-install not supported on %s", runtime.GOOS)
}

// Uninstall unloads and removes the plist (mac); on Linux callers
// remove the systemd unit by hand.
func Uninstall() (Status, error) {
	if runtime.GOOS != "darwin" {
		return Probe(), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Probe(), err
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
	if _, err := os.Stat(plist); err == nil {
		// Best-effort unload. launchctl unload -w persists the disable
		// state even if the file is removed afterward, but `bootout` is
		// the modern equivalent — try both for compat with older macs.
		_ = exec.Command("launchctl", "bootout", "gui/"+uid(), plist).Run()
		_ = exec.Command("launchctl", "unload", "-w", plist).Run()
		_ = exec.Command("launchctl", "remove", Label).Run()
		if err := os.Remove(plist); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Probe(), fmt.Errorf("remove %s: %w", plist, err)
		}
	}
	// Final kill in case the daemon was started manually outside launchd.
	_ = exec.Command("pkill", "-TERM", "-x", "ccmuxd").Run()
	return Probe(), nil
}

// UnitFile returns the printable systemd-user unit for Linux users.
// They write it to ~/.config/systemd/user/ccmuxd.service and
// `systemctl --user enable --now ccmuxd` it.
func UnitFile(binary string) string {
	return fmt.Sprintf(`[Unit]
Description=ccmux daemon (Claude Code session supervisor)
After=default.target

[Service]
ExecStart=%s
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
`, binary)
}

// PlistPathOrEmpty is exported for the main uninstall flow so it can
// preview/remove the file without re-implementing path resolution.
func PlistPathOrEmpty() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if runtime.GOOS != "darwin" {
		return ""
	}
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
}

// installDarwin writes the plist and loads it. Returns the post-load
// status. If ccmuxd isn't installed at the expected path it bails
// early so the user fixes the install first.
func installDarwin() (Status, error) {
	s := Probe()
	if !s.BinaryInstalled {
		return s, fmt.Errorf("ccmuxd not found at %s — run `make install` first", s.BinaryPath)
	}
	home, _ := os.UserHomeDir()
	logsDir := filepath.Join(home, ".local", "state", "ccmux")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return s, err
	}
	if err := os.MkdirAll(filepath.Dir(s.PlistPath), 0o755); err != nil {
		return s, err
	}
	var buf strings.Builder
	if err := plistTemplate.Execute(&buf, plistData{
		Label:       Label,
		Binary:      s.BinaryPath,
		StdoutPath:  filepath.Join(logsDir, "ccmuxd.stdout.log"),
		StderrPath:  filepath.Join(logsDir, "ccmuxd.stderr.log"),
		HomeDir:     home,
		WorkingDir:  home,
	}); err != nil {
		return s, err
	}
	if err := os.WriteFile(s.PlistPath, []byte(buf.String()), 0o644); err != nil {
		return s, err
	}

	// If it was already loaded, unload first so launchctl picks up any
	// changes to the plist (e.g. binary path moved).
	_ = exec.Command("launchctl", "unload", "-w", s.PlistPath).Run()
	if out, err := exec.Command("launchctl", "load", "-w", s.PlistPath).CombinedOutput(); err != nil {
		return s, fmt.Errorf("launchctl load: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return Probe(), nil
}

type plistData struct {
	Label      string
	Binary     string
	StdoutPath string
	StderrPath string
	HomeDir    string
	WorkingDir string
}

// plistTemplate produces a minimal launchd agent definition. KeepAlive
// is true so the daemon restarts if it crashes; RunAtLoad covers
// login + reboot. EnvironmentVariables.HOME is set so the daemon can
// find ~/.config/ccmux/config.toml regardless of launchd's stripped
// environment.
var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>{{.Label}}</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{.Binary}}</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>WorkingDirectory</key>
  <string>{{.WorkingDir}}</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key>
    <string>{{.HomeDir}}</string>
    <key>PATH</key>
    <string>{{.HomeDir}}/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
  </dict>
  <key>StandardOutPath</key>
  <string>{{.StdoutPath}}</string>
  <key>StandardErrorPath</key>
  <string>{{.StderrPath}}</string>
  <key>ProcessType</key>
  <string>Background</string>
</dict>
</plist>
`))

// uid returns the current user's UID as a string — needed for
// `launchctl bootout gui/$UID`. Falls back to "$(id -u)" via shell on
// the off-chance os/user.Current() is broken in this environment.
func uid() string {
	if u := os.Getenv("UID"); u != "" {
		return u
	}
	if out, err := exec.Command("id", "-u").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return "501" // sane default for the canonical Mac
}
