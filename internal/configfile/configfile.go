// Package configfile is a tiny shared helper for the three agent-config
// packages (claudeconfig, codexconfig, antigravityconfig) so they all
// write atomically and rotate backups identically. The packages
// themselves still own their typed Settings round-trip — this is just
// the "write file, keep N backups" plumbing.
package configfile

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MaxBackupsPerFile caps the number of timestamped backups kept per
// settings file. Each write creates one backup; without a cap, heavy
// TUI users accumulate thousands of files (one per toggle).
const MaxBackupsPerFile = 50

// WriteAtomic writes `data` to `dst` via a sibling temp file + fsync +
// rename. Atomic on the same filesystem: a crash mid-write leaves
// either the previous content or the new content, never half.
func WriteAtomic(dst string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".configfile-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleaned := false
	defer func() {
		if !cleaned {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}
	cleaned = true
	return nil
}

// Backup copies `src` to <backupDir>/<basename>.<timestamp> and prunes
// older backups for the same basename beyond MaxBackupsPerFile.
// Idempotent on missing src (no-op, returns "").
func Backup(src, backupDir string) (string, error) {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return "", nil
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	ts := time.Now().Format("20060102-150405.000")
	base := filepath.Base(src)
	dst := filepath.Join(backupDir, base+"."+ts)
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return dst, err
	}
	pruneBackups(backupDir, base, MaxBackupsPerFile)
	return dst, nil
}

func pruneBackups(dir, base string, keep int) {
	if keep <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := base + "."
	matches := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	if len(matches) <= keep {
		return
	}
	sort.Strings(matches)
	for _, name := range matches[:len(matches)-keep] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}
