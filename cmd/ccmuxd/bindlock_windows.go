//go:build windows

package main

import "time"

// bindLock (windows) — the Unix-socket bind path this lock guards is
// unix-only, so the Windows build gets a no-op that always acquires.
// Exists solely to keep GOOS=windows compiling.
type bindLock struct{}

func acquireBindLock(path string, timeout time.Duration) (*bindLock, error) {
	return &bindLock{}, nil
}

func (l *bindLock) release() {}
