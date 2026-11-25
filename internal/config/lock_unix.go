//go:build !windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const updateLockTimeout = 5 * time.Second

// acquireUpdateLock takes an exclusive advisory flock on a sibling lock
// file next to config.toml so concurrent CLI, TUI, and daemon config
// updates cannot clobber each other across processes.
func acquireUpdateLock(configPath string) (func(), error) {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create config dir %q: %w", dir, err)
	}
	lockPath := configPath + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open config lock %q: %w", lockPath, err)
	}
	deadline := time.Now().Add(updateLockTimeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = f.Close()
			return nil, fmt.Errorf("flock config %q: %w", lockPath, err)
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("config lock %q held by another process", lockPath)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
