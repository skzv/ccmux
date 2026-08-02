package sleeplock

import (
	"os"
	"slices"
	"strconv"
	"testing"
)

// TestStartLockProcFor_DarwinTiesAssertionToDaemonPid — regression for
// orphaned caffeinate processes. `launchctl kickstart -k` (used by
// `ccmux daemon restart` / `update`) SIGKILLs the daemon, so no defer
// ever kills the holder; without `-w <daemon pid>` the caffeinate kept
// the sleep assertion forever. Every darwin mode must carry -w with
// this process's pid.
func TestStartLockProcFor_DarwinTiesAssertionToDaemonPid(t *testing.T) {
	pid := strconv.Itoa(os.Getpid())
	cases := []struct {
		mode      Mode
		wantFlags []string
	}{
		{ModeSafe, []string{"-s"}},
		{ModeDangerous, []string{"-d", "-i", "-m", "-s"}},
		{ModeVeryDangerous, []string{"-d", "-i", "-m", "-s"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			cmd := startLockProcFor("darwin", tc.mode)
			if cmd == nil {
				t.Fatalf("startLockProcFor(darwin, %s) = nil", tc.mode)
			}
			args := cmd.Args
			if args[0] != "caffeinate" {
				t.Fatalf("binary = %q, want caffeinate (args %v)", args[0], args)
			}
			wi := slices.Index(args, "-w")
			if wi < 0 || wi+1 >= len(args) {
				t.Fatalf("missing -w <pid> in args %v", args)
			}
			if args[wi+1] != pid {
				t.Errorf("-w pid = %q, want %q (the daemon's own pid)", args[wi+1], pid)
			}
			for _, f := range tc.wantFlags {
				if !slices.Contains(args, f) {
					t.Errorf("missing flag %s in args %v", f, args)
				}
			}
		})
	}
}

// TestStartLockProcFor_OffAndUnsupported — ModeOff and unsupported
// GOOSes must produce no holder at all.
func TestStartLockProcFor_OffAndUnsupported(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		if cmd := startLockProcFor(goos, ModeOff); cmd != nil {
			t.Errorf("startLockProcFor(%s, off) = %v, want nil", goos, cmd.Args)
		}
	}
	for _, mode := range []Mode{ModeSafe, ModeDangerous, ModeVeryDangerous} {
		if cmd := startLockProcFor("windows", mode); cmd != nil {
			t.Errorf("startLockProcFor(windows, %s) = %v, want nil", mode, cmd.Args)
		}
	}
}

// TestStartLockProcFor_LinuxShape pins the systemd-inhibit invocation
// (who/what/why + the sleep-infinity holder child).
func TestStartLockProcFor_LinuxShape(t *testing.T) {
	cmd := startLockProcFor("linux", ModeSafe)
	if cmd == nil {
		t.Fatal("startLockProcFor(linux, safe) = nil")
	}
	if cmd.Args[0] != "systemd-inhibit" {
		t.Errorf("binary = %q, want systemd-inhibit", cmd.Args[0])
	}
	if !slices.Contains(cmd.Args, "--what=sleep:idle") {
		t.Errorf("missing --what=sleep:idle in %v", cmd.Args)
	}
}
