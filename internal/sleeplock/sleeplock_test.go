package sleeplock

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func TestParseMode(t *testing.T) {
	cases := []struct {
		in   string
		want Mode
	}{
		{"", ModeSafe},
		{"safe", ModeSafe},
		{"SAFE", ModeSafe},
		{"  safe  ", ModeSafe},
		{"dangerous", ModeDangerous},
		{"Dangerous", ModeDangerous},
		{"very_dangerous", ModeVeryDangerous},
		{"very-dangerous", ModeVeryDangerous},
		{"VeryDangerous", ModeVeryDangerous},
		{"off", ModeOff},
		{"bogus", ModeSafe}, // unknown falls back to safe
		{"yolo", ModeSafe},
	}
	for _, tc := range cases {
		if got := ParseMode(tc.in); got != tc.want {
			t.Errorf("ParseMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// fakeLockCmd returns a long-running `sleep` command — a stand-in for
// caffeinate / systemd-inhibit that's safe to spawn and kill in tests.
func fakeLockCmd() *exec.Cmd {
	return exec.Command("sleep", "60")
}

// TestManager_EngageRelease covers the basic on/off flow. After
// SetActive(true) the effective mode matches the request; after
// SetActive(false) it's off and the lock process is dead.
func TestManager_EngageRelease(t *testing.T) {
	m := NewManager(ModeSafe, 0)
	m.startLockProc = func(Mode) *exec.Cmd { return fakeLockCmd() }
	m.readBattery = func(context.Context) (BatteryStatus, error) {
		return BatteryStatus{HasBattery: false}, nil
	}

	m.SetActive(true)
	if got := m.Effective(); got != ModeSafe {
		t.Fatalf("Effective after SetActive(true) = %q, want safe", got)
	}
	pid := m.holder.Process.Pid
	m.SetActive(false)
	if got := m.Effective(); got != ModeOff {
		t.Fatalf("Effective after SetActive(false) = %q, want off", got)
	}
	// Holder process must be reaped — sending signal 0 fails on dead
	// PIDs.
	if err := killCheck(pid); err == nil {
		t.Errorf("lock process %d still alive after release", pid)
	}
}

// TestManager_VeryDangerous_SudoSuccess engages very_dangerous with a
// successful (fake) sudo override and verifies overrideOn is set and
// the release path reverts it.
func TestManager_VeryDangerous_SudoSuccess(t *testing.T) {
	overrideCalls := []bool{}
	m := NewManager(ModeVeryDangerous, 0)
	m.startLockProc = func(Mode) *exec.Cmd { return fakeLockCmd() }
	m.runOverride = func(_ context.Context, on bool) error {
		overrideCalls = append(overrideCalls, on)
		return nil
	}
	m.readBattery = func(context.Context) (BatteryStatus, error) {
		return BatteryStatus{HasBattery: false}, nil
	}

	m.SetActive(true)
	if got := m.Effective(); got != ModeVeryDangerous {
		t.Fatalf("Effective = %q, want very_dangerous", got)
	}
	m.SetActive(false)
	if got := overrideCalls; len(got) != 2 || got[0] != true || got[1] != false {
		t.Errorf("override toggles = %v, want [true false]", got)
	}
}

// TestManager_VeryDangerous_SudoFails_DegradesToDangerous — the sudo
// path returning an error must not abort the engage; we degrade to
// dangerous so the user at least gets idle-sleep protection.
func TestManager_VeryDangerous_SudoFails_DegradesToDangerous(t *testing.T) {
	m := NewManager(ModeVeryDangerous, 0)
	m.startLockProc = func(Mode) *exec.Cmd { return fakeLockCmd() }
	m.runOverride = func(context.Context, bool) error { return errors.New("sudo: a password is required") }
	m.readBattery = func(context.Context) (BatteryStatus, error) {
		return BatteryStatus{HasBattery: false}, nil
	}

	m.SetActive(true)
	if got := m.Effective(); got != ModeDangerous {
		t.Fatalf("Effective with sudo fail = %q, want dangerous", got)
	}
	m.SetActive(false)
}

// TestManager_DowngradeOnLowBattery is the safety mechanic. Engage
// dangerous on a laptop showing 5% on battery, expect immediate
// downgrade to safe.
func TestManager_DowngradeOnLowBattery(t *testing.T) {
	var batteryMu sync.Mutex
	battery := BatteryStatus{HasBattery: true, OnAC: false, Percent: 5}

	m := NewManager(ModeDangerous, 20)
	m.startLockProc = func(Mode) *exec.Cmd { return fakeLockCmd() }
	m.runOverride = func(context.Context, bool) error { return nil }
	m.readBattery = func(context.Context) (BatteryStatus, error) {
		batteryMu.Lock()
		defer batteryMu.Unlock()
		return battery, nil
	}

	m.SetActive(true)
	// First poll runs synchronously inside the monitor goroutine. Spin
	// up to 2 seconds — generous so a slow CI host doesn't flake.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m.Effective() == ModeSafe {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := m.Effective(); got != ModeSafe {
		t.Errorf("Effective after low-battery poll = %q, want safe", got)
	}
	m.SetActive(false)
}

// TestManager_NoDowngradeWhenOnAC — battery-monitor must not fire when
// the laptop is plugged in. Even at 1%, AC means "user is at a desk,
// they'll notice if anything's wrong".
func TestManager_NoDowngradeWhenOnAC(t *testing.T) {
	m := NewManager(ModeDangerous, 20)
	m.startLockProc = func(Mode) *exec.Cmd { return fakeLockCmd() }
	m.runOverride = func(context.Context, bool) error { return nil }
	m.readBattery = func(context.Context) (BatteryStatus, error) {
		return BatteryStatus{HasBattery: true, OnAC: true, Percent: 1}, nil
	}

	m.SetActive(true)
	// Give the monitor goroutine a moment to run its synchronous first
	// check.
	time.Sleep(100 * time.Millisecond)
	if got := m.Effective(); got != ModeDangerous {
		t.Errorf("Effective on AC at 1%% = %q, want dangerous (no downgrade)", got)
	}
	m.SetActive(false)
}

// TestManager_NoBatteryMachine — desktops and Mac minis report
// HasBattery=false. The monitor must NOT downgrade them.
func TestManager_NoBatteryMachine(t *testing.T) {
	m := NewManager(ModeDangerous, 20)
	m.startLockProc = func(Mode) *exec.Cmd { return fakeLockCmd() }
	m.runOverride = func(context.Context, bool) error { return nil }
	m.readBattery = func(context.Context) (BatteryStatus, error) {
		return BatteryStatus{HasBattery: false}, nil
	}

	m.SetActive(true)
	time.Sleep(100 * time.Millisecond)
	if got := m.Effective(); got != ModeDangerous {
		t.Errorf("Effective on desktop = %q, want dangerous", got)
	}
	m.SetActive(false)
}

// TestManager_StopReleasesEverything — Stop() must kill the holder and
// revert any override, regardless of which mode we were in.
func TestManager_StopReleasesEverything(t *testing.T) {
	overrideReverted := false
	m := NewManager(ModeVeryDangerous, 0)
	m.startLockProc = func(Mode) *exec.Cmd { return fakeLockCmd() }
	m.runOverride = func(_ context.Context, on bool) error {
		if !on {
			overrideReverted = true
		}
		return nil
	}
	m.readBattery = func(context.Context) (BatteryStatus, error) {
		return BatteryStatus{HasBattery: false}, nil
	}

	m.SetActive(true)
	pid := m.holder.Process.Pid
	m.Stop()
	if m.Effective() != ModeOff {
		t.Errorf("Effective after Stop = %q, want off", m.Effective())
	}
	if !overrideReverted {
		t.Errorf("override not reverted on Stop")
	}
	if err := killCheck(pid); err == nil {
		t.Errorf("lock process %d still alive after Stop", pid)
	}
	// Second Stop must be a clean no-op.
	m.Stop()
}

// TestManager_RepeatedSetActive — idempotency on both sides.
func TestManager_RepeatedSetActive(t *testing.T) {
	starts := 0
	m := NewManager(ModeSafe, 0)
	m.startLockProc = func(Mode) *exec.Cmd {
		starts++
		return fakeLockCmd()
	}
	m.runOverride = func(context.Context, bool) error { return nil }
	m.readBattery = func(context.Context) (BatteryStatus, error) {
		return BatteryStatus{HasBattery: false}, nil
	}

	m.SetActive(true)
	m.SetActive(true)
	m.SetActive(true)
	if starts != 1 {
		t.Errorf("startLockProc called %d times, want 1", starts)
	}
	m.SetActive(false)
	m.SetActive(false)
}

// killCheck returns nil if the process at pid is reachable (alive) by
// signal 0, error otherwise. Used only as a "did our test kill it?"
// probe — not a graceful shutdown wait.
func killCheck(pid int) error {
	return exec.Command("kill", "-0", itoa(pid)).Run()
}

func itoa(i int) string {
	// avoid pulling in strconv for one call
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
