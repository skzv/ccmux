package muse

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Delete removes only a complete inactive native session. Holding Muse's own
// lock prevents racing its writer; rename detaches the selected directory
// before recursive removal, so a later session open cannot be removed with it.
func Delete(home, id, path string) error {
	root := SessionsRoot(home)
	dir, err := SessionDir(root, path)
	if err != nil {
		return err
	}
	if filepath.Base(dir) != id {
		return fmt.Errorf("Muse session ID does not match path")
	}
	// Native companions can contain directories; reject links before touching
	// any data and never follow them during removal.
	check := func(dir string) error {
		return filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink in Muse data: %s", p)
			}
			return nil
		})
	}
	if err := check(dir); err != nil {
		return err
	}
	cache := filepath.Join(root, ".msp-view-v1", id)
	cacheExists := false
	if info, err := os.Lstat(filepath.Dir(cache)); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symlink in Muse cache root")
	}
	if _, err := os.Lstat(cache); err == nil {
		cacheExists = true
		if err := check(cache); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	unlock, err := lockSession(filepath.Join(dir, ".session.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	var childUnlocks []func()
	defer func() {
		for _, release := range childUnlocks {
			release()
		}
	}()
	if err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Name() == ".session.lock" && p != filepath.Join(dir, ".session.lock") {
			release, err := lockSession(p)
			if err != nil {
				return err
			}
			childUnlocks = append(childUnlocks, release)
		}
		return nil
	}); err != nil {
		return err
	}
	// Confine all renames/removal to the opened native root, including against
	// ancestor replacement while the filesystem is changing.
	confined, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer confined.Close()
	tomb, err := os.MkdirTemp(root, ".ccmux-delete-")
	if err != nil {
		return err
	}
	tomb = filepath.Base(tomb)
	rel, _ := filepath.Rel(root, dir)
	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("Muse path escaped root")
	}
	if err := confined.Rename(rel, filepath.Join(tomb, "session")); err != nil {
		_ = confined.Remove(tomb)
		return err
	}
	if cacheExists {
		if err := confined.Rename(filepath.Join(".msp-view-v1", id), filepath.Join(tomb, "cache")); err != nil {
			if restore := confined.Rename(filepath.Join(tomb, "session"), rel); restore != nil {
				return fmt.Errorf("detach Muse cache: %v; session retained at %s (restore: %v)", err, filepath.Join(root, tomb), restore)
			}
			_ = confined.Remove(tomb)
			return err
		}
	}
	if err := confined.RemoveAll(tomb); err != nil {
		return fmt.Errorf("remove Muse session data at %s: %w", filepath.Join(root, tomb), err)
	}
	return nil
}
