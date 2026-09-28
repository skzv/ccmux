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
//     SIGINT/SIGTERM via the daemon's defer chain; an override a killed
//     daemon left behind is reverted by the next one at startup
//     (RevertStaleOverride), whatever mode that one runs in.
//
// The package is engineered so the sudo path and the battery readers
// are swappable for tests via fields on Manager.
package sleeplock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
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

	holder      *exec.Cmd     // the running caffeinate / systemd-inhibit
	holderDone  chan struct{} // closed once holder has exited and been reaped
	overrideOn  bool          // we've issued the very_dangerous system override
	stopMonitor chan struct{}
	monitorWG   sync.WaitGroup
	// stopped is set by Stop. A stopped Manager ignores SetActive, so a
	// poll tick still finishing during shutdown can't re-engage the lock
	// (or re-apply the system override) after Stop released it. It also
	// keeps monitorWG.Add from racing Stop's Wait.
	stopped bool
	// overrideMarker is a file that exists while this Manager has the
	// very_dangerous system override applied (see SetOverrideMarker).
	// Empty disables the bookkeeping.
	overrideMarker string

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

// SetOverrideMarker names a file the Manager keeps in place for exactly
// as long as it has the very_dangerous system override applied, so a
// later daemon can tell that a predecessor died holding it — even after
// the user switched to another mode. Call before RevertStaleOverride
// and the first SetActive.
func (m *Manager) SetOverrideMarker(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.overrideMarker = path
}

// SetActive flips the lock on/off based on whether any session needs to
// keep the system awake. Idempotent — repeated true calls don't spawn
// new holders, repeated false calls don't error. A no-op once Stop has
// run.
func (m *Manager) SetActive(active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return
	}
	if active {
		m.engageLocked()
	} else {
		m.releaseLocked()
	}
}

// engageLocked is the on-transition path. Caller holds m.mu.
func (m *Manager) engageLocked() {
	if m.holder != nil {
		select {
		case <-m.holderDone:
			// The holder died on its own (killed externally, crashed).
			// Forget it and start a fresh one below; without this the
			// lock would stay silently disengaged for the daemon's
			// whole lifetime while reporting itself active.
			m.holder, m.holderDone = nil, nil
		default:
			return // already engaged
		}
	}
	mode := m.requested
	if mode == ModeOff {
		return
	}
	// Try the system override first when very_dangerous; if sudo is not
	// passwordless we silently degrade to dangerous so the user at
	// least gets idle-sleep protection.
	if mode == ModeVeryDangerous && !m.overrideOn {
		if err := m.override(true); err == nil {
			m.overrideOn = true
			m.writeOverrideMarker()
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
	if err := m.startHolderLocked(cmd); err != nil {
		// Same stranded-override risk on a Start() failure right after a
		// successful sudo override.
		m.revertOverrideLocked()
		m.effective = ModeOff
		return
	}
	m.effective = mode
	// Dangerous and very_dangerous both need the battery monitor — the
	// monitor downgrades them when on battery and below cutoff.
	if (m.effective == ModeDangerous || m.effective == ModeVeryDangerous) && m.cutoff > 0 && m.stopMonitor == nil {
		m.startMonitorLocked()
	}
}

// killHolderLocked terminates the lock holder — the whole process
// group when it was started with Setpgid (linux systemd-inhibit, so
// its `sleep infinity` child dies too), just the process otherwise —
// and reaps it. No-op when no holder is running. Caller holds m.mu.
func (m *Manager) killHolderLocked() {
	if m.holder == nil {
		return
	}
	if !killProcessGroup(m.holder) {
		_ = m.holder.Process.Kill()
	}
	<-m.holderDone
	m.holder, m.holderDone = nil, nil
}

// startHolderLocked starts cmd as the lock holder and reaps it in the
// background, closing holderDone when it exits — which is how
// engageLocked notices a holder that died on its own. Caller holds m.mu.
func (m *Manager) startHolderLocked(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	m.holder, m.holderDone = cmd, done
	return nil
}

// RevertStaleOverride clears a very_dangerous system sleep override
// left behind by a previous daemon that died without running Stop
// (SIGKILL, crash, power loss) — overrideOn only lives in memory, so a
// fresh Manager can't know one is in force. Call once at startup,
// before the first SetActive.
//
// It reverts when very_dangerous is requested (the previous daemon may
// predate the marker file) or when the override marker shows a previous
// daemon applied one — whatever mode is configured now, so a user who
// switched to safe after the crash still gets system sleep back. It
// doesn't run sudo otherwise: most users never configured it, and a
// `sudo -n` on every start would log an auth failure (or, for a user
// outside sudoers, an "incident" report) each time.
func (m *Manager) RevertStaleOverride() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.overrideOn {
		return
	}
	if m.requested != ModeVeryDangerous && !m.overrideMarked() {
		return
	}
	if err := m.override(false); err == nil {
		m.removeOverrideMarker()
	}
}

// overrideTimeout bounds one sudo override call. runOverride runs under
// m.mu, so a hung `sudo`/`pmset` would otherwise block every SetActive
// (the poll loop), Effective (the health endpoint) and Stop.
const overrideTimeout = 15 * time.Second

// override applies (on=true) or reverts the system override, bounded by
// overrideTimeout. Caller holds m.mu.
func (m *Manager) override(on bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), overrideTimeout)
	defer cancel()
	return m.runOverride(ctx, on)
}

// overrideMarked reports whether the override marker file exists.
func (m *Manager) overrideMarked() bool {
	if m.overrideMarker == "" {
		return false
	}
	_, err := os.Stat(m.overrideMarker)
	return err == nil
}

// writeOverrideMarker records that the system override is applied. Best
// effort: without it a crash is still recovered while the mode stays
// very_dangerous.
func (m *Manager) writeOverrideMarker() {
	if m.overrideMarker == "" {
		return
	}
	_ = os.WriteFile(m.overrideMarker, []byte("ccmuxd applied the very_dangerous sleep override\n"), 0o600)
}

// removeOverrideMarker clears the marker once the override is reverted.
func (m *Manager) removeOverrideMarker() {
	if m.overrideMarker == "" {
		return
	}
	_ = os.Remove(m.overrideMarker)
}

// releaseLocked is the off-transition path. Caller holds m.mu.
func (m *Manager) releaseLocked() {
	m.killHolderLocked()
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
	// The marker stays when the revert fails, so the next daemon
	// retries it at startup.
	if err := m.override(false); err == nil {
		m.removeOverrideMarker()
	}
	m.overrideOn = false
}

// Stop tears down everything: kills the lock process, reverts the
// system override if any, stops the battery monitor. After Stop the
// Manager ignores SetActive. Idempotent; safe to defer from main and to
// call repeatedly.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.stopped = true
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
	m.killHolderLocked()
	m.revertOverrideLocked()
	cmd := m.startLockProc(ModeSafe)
	if cmd == nil {
		m.effective = ModeOff
		return
	}
	if err := m.startHolderLocked(cmd); err != nil {
		m.effective = ModeOff
		return
	}
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
	return startLockProcFor(runtime.GOOS, mode)
}

// startLockProcFor is the OS-parameterized command builder — split from
// startLockProc so tests can pin the darwin and linux shapes from any
// host without spawning a real caffeinate/systemd-inhibit.
//
// Leak-proofing (regression: sleep blockers outliving the daemon):
//
//   - darwin: `caffeinate -w <daemon pid>` ties the assertion to this
//     process's lifetime. Without it, an ungraceful daemon death
//     (`launchctl kickstart -k` during `ccmux daemon restart`/`update`
//     sends SIGKILL — no defer runs) orphaned caffeinate holding the
//     sleep assertion forever.
//   - linux: the holder starts in its own process group so release can
//     kill the whole group — otherwise killing the systemd-inhibit
//     parent orphaned one `sleep infinity` child per engage/release
//     cycle.
func startLockProcFor(goos string, mode Mode) *exec.Cmd {
	switch goos {
	case "darwin":
		pid := strconv.Itoa(os.Getpid())
		switch mode {
		case ModeSafe:
			// nocontext: the lock holder runs until released; killHolderLocked ends it.
			return exec.Command("caffeinate", "-w", pid, "-s")
		case ModeDangerous, ModeVeryDangerous:
			// -d display, -i idle, -m disk, -s system. Works on battery.
			// nocontext: the lock holder runs until released; killHolderLocked ends it.
			return exec.Command("caffeinate", "-w", pid, "-d", "-i", "-m", "-s")
		}
	case "linux":
		var cmd *exec.Cmd
		switch mode {
		case ModeSafe:
			// nocontext: the lock holder runs until released; killHolderLocked ends it.
			cmd = exec.Command("systemd-inhibit",
				"--what=sleep:idle",
				"--who=ccmuxd", "--why=Claude session active",
				"sleep", "infinity")
		case ModeDangerous, ModeVeryDangerous:
			// Also block handle-lid-switch so a lid-close on battery
			// doesn't catch us; on most laptops this works without
			// sudo via systemd-inhibit.
			// nocontext: the lock holder runs until released; killHolderLocked ends it.
			cmd = exec.Command("systemd-inhibit",
				"--what=sleep:idle:handle-lid-switch",
				"--who=ccmuxd", "--why=Claude session active (dangerous mode)",
				"sleep", "infinity")
		}
		if cmd != nil {
			setNewProcessGroup(cmd)
		}
		return cmd
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
