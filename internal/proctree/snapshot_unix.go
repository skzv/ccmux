//go:build unix

package proctree

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// snapshotTimeout bounds one ps run. It lists a few thousand processes
// in tens of milliseconds; a ps that takes longer is wedged, and the
// caller carries on without a snapshot.
const snapshotTimeout = 3 * time.Second

// Snapshot runs ps once and parses the whole process table.
func Snapshot(ctx context.Context) (*Table, error) {
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", psArgs...)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w%s", err, stderrOf(err))
	}
	t := Parse(out)
	if t.Len() == 0 {
		return nil, errors.New("ps: no processes in its output")
	}
	return t, nil
}

// stderrOf is ps's own complaint, when it exited with one.
func stderrOf(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if msg := strings.TrimSpace(string(ee.Stderr)); msg != "" {
			return " (" + msg + ")"
		}
	}
	return ""
}
