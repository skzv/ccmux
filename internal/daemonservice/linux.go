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

// linuxUnitPath is where the systemd-user unit lives under home.
func linuxUnitPath(home string) string {
	return filepath.Join(home, ".config", "systemd", "user", "ccmuxd.service")
}

// probeLinux fills in the systemd-user unit + is-enabled/is-active state.
func probeLinux(s *Status, home string) {
	s.ServicePath = linuxUnitPath(home)
	if _, err := os.Stat(s.ServicePath); err == nil {
		s.ServiceExists = true
	}
	if !hasSystemdUser() {
		return
	}
	// `systemctl --user is-enabled ccmuxd` exits 0 on enabled, non-zero
	// otherwise. We don't care about the text.
	if err := runCmd("systemctl", "--user", "is-enabled", "ccmuxd"); err == nil {
		s.ServiceEnabled = true
	}
}

// installLinux writes the unit file, enables it, and (re)starts it.
func installLinux() (Status, error) {
	if !hasSystemdUser() {
		return Probe(), errors.New("systemd-user not available on this host (no systemctl, or your distro doesn't run a user manager). Print the unit with `ccmux daemon unit` and install it manually for your init system.")
	}
	s := Probe()
	if err := requireBinary(s); err != nil {
		return s, err
	}
	home, _ := os.UserHomeDir()
	cfg, _ := config.Load()
	if err := newInstaller(home, s.BinaryPath, cfg.AgentCommands()).installLinux(); err != nil {
		return s, err
	}
	return Probe(), nil
}

// installLinux renders the unit under in.home — replacing any existing
// one — then reloads systemd, enables the unit, and restarts it.
func (in installer) installLinux() error {
	if _, err := ensureStateDirs(in.home); err != nil {
		return err
	}
	unitPath := linuxUnitPath(in.home)
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return err
	}
	body := renderUnitFile(in.binary, managedPath(in.home, in.commands, "/usr/local/bin", "/usr/bin", "/bin"), true, passthroughEnv(in.getenv))
	if err := os.WriteFile(unitPath, []byte(body), 0o644); err != nil {
		return err
	}
	if out, err := in.run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	// Enable for boot-time activation, then restart rather than
	// `enable --now`: --now only starts a stopped unit, so re-installing
	// over a running daemon would leave it on the old binary path and
	// Environment= lines until the next login. restart starts the unit
	// when it isn't running yet.
	if out, err := in.run("systemctl", "--user", "enable", "ccmuxd"); err != nil {
		return fmt.Errorf("systemctl enable: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if out, err := in.run("systemctl", "--user", "restart", "ccmuxd"); err != nil {
		return fmt.Errorf("systemctl restart: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// restartLinux uses systemctl --user restart. If the user manager
// isn't reachable, falls back to a SIGTERM so any manually-launched
// ccmuxd at least dies (caller can re-spawn).
func restartLinux() (Status, error) {
	s := Probe()
	if !hasSystemdUser() {
		_ = runCmd("pkill", "-TERM", "-U", uid(), "-x", "ccmuxd")
		return Probe(), errors.New("systemd-user not available; sent SIGTERM and bailed — restart ccmuxd by hand")
	}
	if !s.ServiceEnabled {
		_ = runCmd("pkill", "-TERM", "-U", uid(), "-x", "ccmuxd")
		return Probe(), errors.New("ccmuxd not registered with systemd-user; run `ccmux daemon install` first")
	}
	if out, err := combinedCmd("systemctl", "--user", "restart", "ccmuxd"); err != nil {
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
		_ = runCmd("systemctl", "--user", "disable", "--now", "ccmuxd")
	}
	if _, err := os.Stat(unitPath); err == nil {
		if err := removePathQuiet(unitPath); err != nil {
			return Probe(), fmt.Errorf("remove %s: %w", unitPath, err)
		}
		// Reload so systemd forgets the unit immediately.
		if hasSystemdUser() {
			_ = runCmd("systemctl", "--user", "daemon-reload")
		}
	}
	// In case the daemon was started manually outside systemd.
	_ = runCmd("pkill", "-TERM", "-U", uid(), "-x", "ccmuxd")
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
	return runCmd("systemctl", "--user", "show-environment") == nil
}
