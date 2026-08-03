package cmd

import (
	"errors"
	"testing"

	"github.com/skzv/ccmux/internal/daemonservice"
)

// TestUninstallServiceStep_RoutesFailureThroughReport — regression for
// `ccmux uninstall` swallowing a daemonservice.Uninstall() failure: the
// error branch did nothing, so the command printed "Uninstall
// complete." and exited 0 while launchd's KeepAlive kept respawning a
// deleted binary. The step must hand the error to report() so it
// prints ✗ and sets the command's non-zero exit.
func TestUninstallServiceStep_RoutesFailureThroughReport(t *testing.T) {
	boom := errors.New("launchctl bootout failed")
	var gotMsg string
	var gotErr error
	calls := 0
	uninstallServiceStep(
		func() (daemonservice.Status, error) { return daemonservice.Status{}, boom },
		func(msg string, err error) {
			calls++
			gotMsg, gotErr = msg, err
		},
	)
	if calls != 1 {
		t.Fatalf("report called %d times, want 1", calls)
	}
	if !errors.Is(gotErr, boom) {
		t.Errorf("report err = %v, want the uninstall failure", gotErr)
	}
	if gotMsg == "" {
		t.Error("report msg empty")
	}
}

// TestUninstallServiceStep_SuccessReportsOK — the happy path still
// reports a nil-error line.
func TestUninstallServiceStep_SuccessReportsOK(t *testing.T) {
	var gotErr error
	calls := 0
	uninstallServiceStep(
		func() (daemonservice.Status, error) { return daemonservice.Status{}, nil },
		func(msg string, err error) {
			calls++
			gotErr = err
		},
	)
	if calls != 1 {
		t.Fatalf("report called %d times, want 1", calls)
	}
	if gotErr != nil {
		t.Errorf("report err = %v, want nil on success", gotErr)
	}
}
