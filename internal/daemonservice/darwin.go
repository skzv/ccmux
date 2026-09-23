package daemonservice

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/skzv/ccmux/internal/config"
)

// probeDarwin fills in plist + launchctl-load state.
func probeDarwin(s *Status, home string) {
	s.ServicePath = filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
	if _, err := os.Stat(s.ServicePath); err == nil {
		s.ServiceExists = true
	}
	// `launchctl list <label>` prints a plist-style dict when loaded;
	// exits non-zero when not loaded. We only need the exit status.
	if out, err := outputCmd("launchctl", "list", Label); err == nil && len(out) > 0 {
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

	cfg, _ := config.Load()
	var buf strings.Builder
	if err := plistTemplate.Execute(&buf, plistData{
		Label:      Label,
		Binary:     s.BinaryPath,
		StdoutPath: filepath.Join(logsDir, "ccmuxd.stdout.log"),
		StderrPath: filepath.Join(logsDir, "ccmuxd.stderr.log"),
		HomeDir:    home,
		WorkingDir: home,
		Path:       managedPath(home, cfg.AgentCommands(), "/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin", "/usr/bin", "/bin"),
	}); err != nil {
		return s, err
	}
	if err := os.WriteFile(s.ServicePath, []byte(buf.String()), 0o644); err != nil {
		return s, err
	}

	// If already loaded, unload first so launchctl picks up any plist
	// changes (most importantly: a binary path that moved).
	_ = runCmd("launchctl", "unload", "-w", s.ServicePath)
	if out, err := combinedCmd("launchctl", "load", "-w", s.ServicePath); err != nil {
		return s, fmt.Errorf("launchctl load: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return Probe(), nil
}

// restartDarwin uses `launchctl kickstart -k` which signals launchd to
// stop the current daemon and start a fresh one. Returns an error if
// the daemon isn't registered with launchd (i.e. `ccmux daemon install`
// was never run). If the kickstart succeeds but the daemon takes a
// moment to come back, Probe() will still report Running=false; the
// caller should re-probe after a short delay.
func restartDarwin() (Status, error) {
	s := Probe()
	if !s.ServiceEnabled {
		// Not under launchd — try a plain pkill so a manually-started
		// daemon can be restarted by the caller.
		_ = runCmd("pkill", "-TERM", "-U", uid(), "-x", "ccmuxd")
		return Probe(), fmt.Errorf("ccmuxd not registered with launchd; restart by hand or run `ccmux daemon install`")
	}
	target := "gui/" + uid() + "/" + Label
	if out, err := combinedCmd("launchctl", "kickstart", "-k", target); err != nil {
		return s, fmt.Errorf("launchctl kickstart %s: %w (%s)", target, err, strings.TrimSpace(string(out)))
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
		_ = runCmd("launchctl", "bootout", "gui/"+uid(), plist)
		_ = runCmd("launchctl", "unload", "-w", plist)
		_ = runCmd("launchctl", "remove", Label)
		if err := removePathQuiet(plist); err != nil {
			return Probe(), fmt.Errorf("remove %s: %w", plist, err)
		}
	}
	_ = runCmd("pkill", "-TERM", "-U", uid(), "-x", "ccmuxd")
	return Probe(), nil
}

type plistData struct {
	Label      string
	Binary     string
	StdoutPath string
	StderrPath string
	HomeDir    string
	WorkingDir string
	Path       string
}

// plistTemplate renders the LaunchAgent. Every value is piped through
// xml: a home dir or binary path containing `&` or `<` would otherwise
// produce a plist launchd refuses to load.
var plistTemplate = template.Must(template.New("plist").Funcs(template.FuncMap{"xml": xmlEscape}).Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>{{.Label | xml}}</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{.Binary | xml}}</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <!--
    KeepAlive is conditional, not blanket-true: respawn only when the
    last exit was unsuccessful (non-zero). The previous unconditional
    KeepAlive=true paired with a Go-level "another ccmuxd is already
    listening → exit 1" path made launchd respawn the daemon every
    ~10s in a tight loop, spamming the stderr log forever. With this
    dict and ccmuxd's matching "exit 0 when peer already serving"
    shim in main.go, that loop is no longer possible. Crashes still
    trigger a respawn (exit code != 0).
  -->
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
  <key>WorkingDirectory</key>
  <string>{{.WorkingDir | xml}}</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key>
    <string>{{.HomeDir | xml}}</string>
    <key>PATH</key>
    <string>{{.Path | xml}}</string>
  </dict>
  <key>StandardOutPath</key>
  <string>{{.StdoutPath | xml}}</string>
  <key>StandardErrorPath</key>
  <string>{{.StderrPath | xml}}</string>
  <key>ProcessType</key>
  <string>Background</string>
</dict>
</plist>
`))

func xmlEscape(v string) (string, error) {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(v)); err != nil {
		return "", err
	}
	return b.String(), nil
}

// uid returns the current user's UID as a string — needed for
// `launchctl bootout gui/<uid>`. $UID is a shell variable that usually
// isn't exported to launchd jobs, and the old fallback hardcoded 501
// (only right for the first account on a Mac).
func uid() string {
	return strconv.Itoa(os.Getuid())
}
