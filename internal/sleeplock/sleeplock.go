// Package sleeplock owns the daemon's sleep-prevention primitives.
//
// Three modes ship today:
//
//   - safe — `caffeinate -s` (macOS) / `systemd-inhibit --what=sleep:idle`
//     (Linux). On macOS Apple's own policy makes `caffeinate` ignore
//     the lock when on battery + lid-closed, which is exactly what we
//     want as the default: we never accidentally murder a battery.
//
//   - dangerous — `caffeinate -d -i -m -s` (macOS) extends the lock to
//     battery and to the display/idle/disk subsystems. A small battery
//     monitor downgrades back to "safe" when the charge drops below
//     LowBatteryCutoff so a forgotten-on-battery laptop doesn't flatline.
//     Lid-close still puts the system to sleep — that needs Mode 3.
//
//   - very_dangerous — dangerous + `sudo -n pmset -a disablesleep 1`
//     (macOS) / `sudo -n systemctl mask sleep.target suspend.target …`
//     (Linux). Survives lid-close. Requires passwordless sudo for the
//     specific command. Reverted on Manager.Stop() and on
//     SIGINT/SIGTERM via the daemon's defer chain.
//
// The package is engineered so the sudo path and the battery readers
// are swappable for tests via fields on Manager.
package sleeplock

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Mode is the requested aggressiveness level. The empty string and
// any unknown value resolve to ModeSafe.
type Mode string

const (
	ModeOff           Mode = "off"
	ModeSafe          Mode = "safe"
	ModeDangerous     Mode = "dangerous"
	ModeVeryDangerous Mode = "very_dangerous"
)

// ParseMode normalizes user-supplied strings to a known Mode. Unknown
// values become ModeSafe — the conservative default. The legacy
// boolean `dangerous_keep_awake_on_battery=true` is mapped by the
// caller (config layer) by passing "dangerous" in.
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off":
		return ModeOff
	case "", "safe":
		return ModeSafe
	case "dangerous":
		return ModeDangerous
	case "very_dangerous", "very-dangerous", "verydangerous":
		return ModeVeryDangerous
	default:
		return ModeSafe
	}
}

// BatteryStatus is the subset of power info we need to make
// auto-downgrade decisions. Percent is 0-100; OnAC=true when the laptop
// is plugged in (battery monitor doesn't fire in that case).
type BatteryStatus struct {
	Percent int
	OnAC    bool
	// HasBattery is false on machines without a battery (desktops, Mac
	// minis). Dangerous mode skips the monitor on those — there's no
	// battery to flatten.
	HasBattery bool
}

// Manager is the single per-daemon sleep-prevention controller.
//
// Construction: NewManager(mode, cutoff). Wire to lifecycle:
//
//	m := sleeplock.NewManager(sleeplock.ParseMode(cfg.Mode), cfg.LowBatteryCutoff)
//	defer m.Stop()
//	m.SetActive(anyActive)
//
// The manager is safe for concurrent calls. Stop() is idempotent.
type Manager struct {
	mu sync.Mutex

	requested Mode // what the user asked for
	effective Mode // what we're actually running (may downgrade)
	cutoff    int  // LowBatteryCutoff %; 0 disables monitor

	holder      *exec.Cmd // the running caffeinate / systemd-inhibit
	overrideOn  bool      // we've issued the very_dangerous system override
	stopMonitor chan struct{}
	monitorWG   sync.WaitGroup

	// Injectable seams for tests. nil means "use real OS path".
	readBattery   func(ctx context.Context) (BatteryStatus, error)
	runOverride   func(ctx context.Context, on bool) error
	startLockProc func(mode Mode) *exec.Cmd
}

// NewManager builds a manager. cutoff <=0 disables the battery monitor;
// otherwise the monitor polls once a minute and downgrades when on
// battery and below cutoff. Sensible default is 20.
func NewManager(mode Mode, cutoff int) *Manager {
	return &Manager{
		requested:     mode,
		effective:     ModeOff,
		cutoff:        cutoff,
		readBattery:   readBattery,
		runOverride:   runOverride,
		startLockProc: startLockProc,
	}
}

// Requested returns the mode the user asked for (not necessarily what's
// currently active — see Effective).
func (m *Manager) Requested() Mode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requested
}

// Effective returns the mode actually in force right now. May differ
// from Requested if the battery monitor downgraded, or if the sudo
// override for very_dangerous failed (in which case we run as
// dangerous).
func (m *Manager) Effective() Mode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.effective
}

// SetActive flips the lock on/off based on whether any session needs to
// keep the system awake. Idempotent — repeated true calls don't spawn
// new holders, repeated false calls don't error.
func (m *Manager) SetActive(active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if active {
		m.engageLocked()
	} else {
		m.releaseLocked()
	}
}

// engageLocked is the on-transition path. Caller holds m.mu.
func (m *Manager) engageLocked() {
	if m.holder != nil {
		return // already engaged
	}
	mode := m.requested
	if mode == ModeOff {
		return
	}
	// Try the system override first when very_dangerous; if sudo is not
	// passwordless we silently degrade to dangerous so the user at
	// least gets idle-sleep protection.
	if mode == ModeVeryDangerous {
		if err := m.runOverride(context.Background(), true); err == nil {
			m.overrideOn = true
		} else {
			mode = ModeDangerous
		}
	}
	cmd := m.startLockProc(mode)
	if cmd == nil {
		// Lock process unavailable (unsupported OS). If we already
		// applied the very_dangerous system override above, revert it —
		// otherwise we'd leave system sleep globally disabled while
		// reporting Off, and a later SetActive would re-apply it on top.
		m.revertOverrideLocked()
		m.effective = ModeOff
		return
	}
	if err := cmd.Start(); err != nil {
		// Same stranded-override risk on a Start() failure right after a
		// successful sudo override.
		m.revertOverrideLocked()
		m.effective = ModeOff
		return
	}
	m.holder = cmd
	m.effective = mode
	// Dangerous and very_dangerous both need the battery monitor — the
	// monitor downgrades them when on battery and below cutoff.
	if (m.effective == ModeDangerous || m.effective == ModeVeryDangerous) && m.cutoff > 0 && m.stopMonitor == nil {
		m.startMonitorLocked()
	}
}

// releaseLocked is the off-transition path. Caller holds m.mu.
func (m *Manager) releaseLocked() {
	if m.holder != nil {
		_ = m.holder.Process.Kill()
		_, _ = m.holder.Process.Wait()
		m.holder = nil
	}
	m.revertOverrideLocked()
	if m.stopMonitor != nil {
		close(m.stopMonitor)
		m.stopMonitor = nil
	}
	m.effective = ModeOff
}

// revertOverrideLocked undoes the very_dangerous system sleep override
// if one is active and clears the flag. Idempotent. Caller holds m.mu.
// Centralized so the engage-failure paths and releaseLocked can't
// drift on the "did we remember to clear overrideOn" invariant.
func (m *Manager) revertOverrideLocked() {
	if !m.overrideOn {
		return
	}
	_ = m.runOverride(context.Background(), false)
	m.overrideOn = false
}

// Stop tears down everything: kills the lock process, reverts the
// system override if any, stops the battery monitor. Idempotent; safe
// to defer from main and to call repeatedly.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.releaseLocked()
	m.mu.Unlock()
	m.monitorWG.Wait()
}

// downgradeFromDangerous is called by the battery monitor when the
// charge crosses cutoff. We hold the lock at dangerous level today;
// drop to safe (still some protection) and stop the monitor so we
// don't oscillate.
func (m *Manager) downgradeFromDangerous(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.effective != ModeDangerous && m.effective != ModeVeryDangerous {
		return
	}
	// Kill the dangerous holder, start a safe one in its place. The
	// override (if any) is reverted because letting the system pmset
	// override survive a downgrade defeats the whole "fail safe"
	// promise.
	if m.holder != nil {
		_ = m.holder.Process.Kill()
		_, _ = m.holder.Process.Wait()
		m.holder = nil
	}
	if m.overrideOn {
		_ = m.runOverride(context.Background(), false)
		m.overrideOn = false
	}
	cmd := m.startLockProc(ModeSafe)
	if cmd == nil {
		m.effective = ModeOff
		return
	}
	if err := cmd.Start(); err != nil {
		m.effective = ModeOff
		return
	}
	m.holder = cmd
	m.effective = ModeSafe
	// Stop the monitor — we're not at risk of further downgrades.
	if m.stopMonitor != nil {
		close(m.stopMonitor)
		m.stopMonitor = nil
	}
	_ = reason // hook for the daemon to log via the manager event channel later
}

// startMonitorLocked spawns the battery-polling goroutine. Caller holds m.mu.
func (m *Manager) startMonitorLocked() {
	m.stopMonitor = make(chan struct{})
	stop := m.stopMonitor
	m.monitorWG.Add(1)
	go func() {
		defer m.monitorWG.Done()
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		// First check right away — if we engaged on a near-dead
		// battery, downgrade immediately rather than waiting a minute.
		m.checkBatteryOnce()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				m.checkBatteryOnce()
			}
		}
	}()
}

// checkBatteryOnce reads battery status and downgrades if appropriate.
func (m *Manager) checkBatteryOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	bs, err := m.readBattery(ctx)
	if err != nil || !bs.HasBattery || bs.OnAC {
		return
	}
	if bs.Percent <= m.cutoff {
		m.downgradeFromDangerous(fmt.Sprintf("battery %d%% ≤ cutoff %d%%", bs.Percent, m.cutoff))
	}
}

// startLockProc returns the *exec.Cmd that will hold the lock for the
// given mode. Returns nil on an unsupported OS or for ModeOff.
func startLockProc(mode Mode) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		switch mode {
		case ModeSafe:
			return exec.Command("caffeinate", "-s")
		case ModeDangerous, ModeVeryDangerous:
			// -d display, -i idle, -m disk, -s system. Works on battery.
			return exec.Command("caffeinate", "-d", "-i", "-m", "-s")
		}
	case "linux":
		switch mode {
		case ModeSafe:
			return exec.Command("systemd-inhibit",
				"--what=sleep:idle",
				"--who=ccmuxd", "--why=Claude session active",
				"sleep", "infinity")
		case ModeDangerous, ModeVeryDangerous:
			// Also block handle-lid-switch so a lid-close on battery
			// doesn't catch us; on most laptops this works without
			// sudo via systemd-inhibit.
			return exec.Command("systemd-inhibit",
				"--what=sleep:idle:handle-lid-switch",
				"--who=ccmuxd", "--why=Claude session active (dangerous mode)",
				"sleep", "infinity")
		}
	}
	return nil
}

// runOverride toggles the system-wide sleep override used by
// very_dangerous mode. on=true enables, on=false reverts. Uses
// `sudo -n` so we fail fast if passwordless sudo isn't configured —
// no interactive prompt from a background daemon.
func runOverride(ctx context.Context, on bool) error {
	switch runtime.GOOS {
	case "darwin":
		val := "0"
		if on {
			val = "1"
		}
		return exec.CommandContext(ctx, "sudo", "-n", "pmset", "-a", "disablesleep", val).Run()
	case "linux":
		units := []string{"sleep.target", "suspend.target", "hibernate.target", "hybrid-sleep.target"}
		args := []string{"-n", "systemctl"}
		if on {
			args = append(args, "mask")
		} else {
			args = append(args, "unmask")
		}
		args = append(args, units...)
		return exec.CommandContext(ctx, "sudo", args...).Run()
	}
	return errors.New("very_dangerous mode unsupported on this OS")
}
