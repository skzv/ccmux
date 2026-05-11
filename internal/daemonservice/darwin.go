package daemonservice

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

// probeDarwin fills in plist + launchctl-load state.
func probeDarwin(s *Status, home string) {
	s.ServicePath = filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
	if _, err := os.Stat(s.ServicePath); err == nil {
		s.ServiceExists = true
	}
	// `launchctl list <label>` prints a plist-style dict when loaded;
	// exits non-zero when not loaded. We only need the exit status.
	if out, err := exec.Command("launchctl", "list", Label).Output(); err == nil && len(out) > 0 {
		s.ServiceEnabled = true
	}
}

// installDarwin writes the plist and loads it via launchctl.
func installDarwin() (Status, error) {
	s := Probe()
	if err := requireBinary(s); err != nil {
		return s, err
	}
	home, _ := os.UserHomeDir()
	logsDir, err := ensureStateDirs(home)
	if err != nil {
		return s, err
	}
	if err := os.MkdirAll(filepath.Dir(s.ServicePath), 0o755); err != nil {
		return s, err
	}

	var buf strings.Builder
	if err := plistTemplate.Execute(&buf, plistData{
		Label:      Label,
		Binary:     s.BinaryPath,
		StdoutPath: filepath.Join(logsDir, "ccmuxd.stdout.log"),
		StderrPath: filepath.Join(logsDir, "ccmuxd.stderr.log"),
		HomeDir:    home,
		WorkingDir: home,
	}); err != nil {
		return s, err
	}
	if err := os.WriteFile(s.ServicePath, []byte(buf.String()), 0o644); err != nil {
		return s, err
	}

	// If already loaded, unload first so launchctl picks up any plist
	// changes (most importantly: a binary path that moved).
	_ = exec.Command("launchctl", "unload", "-w", s.ServicePath).Run()
	if out, err := exec.Command("launchctl", "load", "-w", s.ServicePath).CombinedOutput(); err != nil {
		return s, fmt.Errorf("launchctl load: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return Probe(), nil
}

func uninstallDarwin() (Status, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Probe(), err
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
	if _, err := os.Stat(plist); err == nil {
		// `bootout gui/$UID` is the modern launchctl API. Fall back to
		// `unload -w` and `remove` for older macOS versions.
		_ = exec.Command("launchctl", "bootout", "gui/"+uid(), plist).Run()
		_ = exec.Command("launchctl", "unload", "-w", plist).Run()
		_ = exec.Command("launchctl", "remove", Label).Run()
		if err := removePathQuiet(plist); err != nil {
			return Probe(), fmt.Errorf("remove %s: %w", plist, err)
		}
	}
	_ = exec.Command("pkill", "-TERM", "-x", "ccmuxd").Run()
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
// the off-chance the env var isn't set.
func uid() string {
	if u := os.Getenv("UID"); u != "" {
		return u
	}
	if out, err := exec.Command("id", "-u").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return "501"
}
