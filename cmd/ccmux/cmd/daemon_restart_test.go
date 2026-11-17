package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/daemonservice"
)

// fakeHealth answers healthy once callers have waited at least `after`
// in total, without sleeping: each call "spends" its whole timeout.
func fakeHealth(after time.Duration) func(time.Duration) bool {
	var spent time.Duration
	return func(timeout time.Duration) bool {
		spent += timeout
		return spent >= after
	}
}

// TestDaemonRestart_SlowFirstLaunchIsNotReportedDown — after `make
// install`, macOS checks the freshly written ccmuxd on its first launch,
// which took ~10s; with the old 8s budget the command printed "ccmuxd is
// not running" while launchd was bringing it up. The default budget must
// cover that, and say it's waiting instead of going quiet.
func TestDaemonRestart_SlowFirstLaunchIsNotReportedDown(t *testing.T) {
	var out bytes.Buffer
	err := runDaemonRestart(&out, daemonRestartDeps{
		restart: func() (daemonservice.Status, error) { return daemonservice.Status{}, nil },
		healthy: fakeHealth(10 * time.Second),
	}, daemonRestartWait)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "✓ ccmuxd restarted") || strings.Contains(got, "not running") {
		t.Errorf("a daemon back after 10s was reported as:\n%s", got)
	}
	if !strings.Contains(got, "waiting for ccmuxd") {
		t.Errorf("no progress line while waiting:\n%s", got)
	}
}

func TestDaemonRestart_Reporting(t *testing.T) {
	cases := []struct {
		name     string
		running  bool
		after    time.Duration // healthy once this much was waited; 0 = at once
		never    bool
		want     string
		wantWait bool
	}{
		{name: "back at once", want: "✓ ccmuxd restarted"},
		{name: "never answers, process up", running: true, never: true, want: "still warming up", wantWait: true},
		{name: "never answers, no process", never: true, want: "ccmuxd is not running", wantWait: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			health := fakeHealth(tc.after)
			if tc.never {
				health = func(time.Duration) bool { return false }
			}
			var out bytes.Buffer
			err := runDaemonRestart(&out, daemonRestartDeps{
				restart: func() (daemonservice.Status, error) { return daemonservice.Status{Running: tc.running}, nil },
				healthy: health,
			}, daemonRestartWait)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("output = %q, want it to contain %q", out.String(), tc.want)
			}
			if got := strings.Contains(out.String(), "waiting for ccmuxd"); got != tc.wantWait {
				t.Errorf("progress line shown = %v, want %v:\n%s", got, tc.wantWait, out.String())
			}
		})
	}
}

func TestDaemonRestart_RestartErrorIsReturned(t *testing.T) {
	boom := errors.New("launchctl kickstart failed")
	err := runDaemonRestart(&bytes.Buffer{}, daemonRestartDeps{
		restart: func() (daemonservice.Status, error) { return daemonservice.Status{}, boom },
		healthy: func(time.Duration) bool { t.Fatal("health polled after a failed restart"); return false },
	}, daemonRestartWait)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the restart error", err)
	}
}
