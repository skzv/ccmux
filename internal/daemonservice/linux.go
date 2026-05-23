package daemonservice

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/skzv/ccmux/internal/config"
)

// probeLinux fills in the systemd-user unit + is-enabled/is-active state.
func probeLinux(s *Status, home string) {
	s.ServicePath = filepath.Join(home, ".config", "systemd", "user", "ccmuxd.service")
	if _, err := os.Stat(s.ServicePath); err == nil {
		s.ServiceExists = true
	}
	if !hasSystemdUser() {
		return
	}
	// `systemctl --user is-enabled ccmuxd` exits 0 on enabled, non-zero
	// otherwise. We don't care about the text.
	if err := exec.Command("systemctl", "--user", "is-enabled", "ccmuxd").Run(); err == nil {
		s.ServiceEnabled = true
	}
}

// installLinux writes the unit file and `systemctl --user enable --now`s it.
func installLinux() (Status, error) {
	if !hasSystemdUser() {
		return Probe(), errors.New("systemd-user not available on this host (no systemctl, or your distro doesn't run a user manager). Print the unit with `ccmux daemon unit` and install it manually for your init system.")
	}
	s := Probe()
	if err := requireBinary(s); err != nil {
		return s, err
	}
	home, _ := os.UserHomeDir()
	if _, err := ensureStateDirs(home); err != nil {
		return s, err
	}
	if err := os.MkdirAll(filepath.Dir(s.ServicePath), 0o755); err != nil {
		return s, err
	}
	cfg, _ := config.Load()
	body := UnitFileWithPath(s.BinaryPath, managedPath(home, cfg.AgentCommands(), "/usr/local/bin", "/usr/bin", "/bin"))
	if err := os.WriteFile(s.ServicePath, []byte(body), 0o644); err != nil {
		return s, err
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return s, fmt.Errorf("systemctl daemon-reload: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	// `enable --now` enables boot-time activation AND starts the unit.
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", "ccmuxd").CombinedOutput(); err != nil {
		return s, fmt.Errorf("systemctl enable --now: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return Probe(), nil
}

// restartLinux uses systemctl --user restart. If the user manager
// isn't reachable, falls back to a SIGTERM so any manually-launched
// ccmuxd at least dies (caller can re-spawn).
func restartLinux() (Status, error) {
	s := Probe()
	if !hasSystemdUser() {
		_ = exec.Command("pkill", "-TERM", "-x", "ccmuxd").Run()
		return Probe(), errors.New("systemd-user not available; sent SIGTERM and bailed — restart ccmuxd by hand")
	}
	if !s.ServiceEnabled {
		_ = exec.Command("pkill", "-TERM", "-x", "ccmuxd").Run()
		return Probe(), errors.New("ccmuxd not registered with systemd-user; run `ccmux daemon install` first")
	}
	if out, err := exec.Command("systemctl", "--user", "restart", "ccmuxd").CombinedOutput(); err != nil {
		return s, fmt.Errorf("systemctl restart: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return Probe(), nil
}

func uninstallLinux() (Status, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Probe(), err
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", "ccmuxd.service")
	if hasSystemdUser() {
		// `disable --now` stops the unit AND removes the boot symlink.
		_ = exec.Command("systemctl", "--user", "disable", "--now", "ccmuxd").Run()
	}
	if _, err := os.Stat(unitPath); err == nil {
		if err := removePathQuiet(unitPath); err != nil {
			return Probe(), fmt.Errorf("remove %s: %w", unitPath, err)
		}
		// Reload so systemd forgets the unit immediately.
		if hasSystemdUser() {
			_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		}
	}
	// In case the daemon was started manually outside systemd.
	_ = exec.Command("pkill", "-TERM", "-x", "ccmuxd").Run()
	return Probe(), nil
}

// hasSystemdUser checks that systemctl is on PATH and that
// `--user` queries work in this session. Some environments (WSL1,
// some Docker containers, headless servers without lingering enabled)
// don't have a user manager bus; we want a clean error there rather
// than a confusing systemctl failure mid-Install.
func hasSystemdUser() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	// `is-system-running` doesn't exist for --user; use `show-environment`
	// which is cheap and returns 0 when the user manager is reachable.
	return exec.Command("systemctl", "--user", "show-environment").Run() == nil
}
