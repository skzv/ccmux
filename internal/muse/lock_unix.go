//go:build darwin || linux

package muse

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func lockSession(path string) (func(), error) {
	// Missing native lock is an unsupported/incomplete session layout. Do not
	// create one and assume it coordinates with the installed Muse version.
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open Muse session lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("Muse session is active or cannot be locked: %w", err)
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
}
