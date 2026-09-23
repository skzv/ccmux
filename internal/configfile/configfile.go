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
//
// `mode` is the most permissive the file may end up: a new file gets
// exactly `mode`, an existing one keeps its own bits intersected with
// `mode`. So a settings.json the user chmod'ed 0600 (it can hold API
// keys) stays 0600, while a caller passing 0600 still tightens an old
// 0644 file.
//
// If dst is a symlink (dotfiles managed by stow/chezmoi), the link's
// target is rewritten and the link itself is left in place — renaming
// over dst would silently replace the link with a regular file.
func WriteAtomic(dst string, data []byte, mode os.FileMode) error {
	if resolved, err := filepath.EvalSymlinks(dst); err == nil {
		dst = resolved
	}
	if info, err := os.Stat(dst); err == nil {
		mode &= info.Mode().Perm()
	}
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
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
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
	// 0600: backups are copies of files that can hold API keys, and
	// they outlive any later chmod of the original.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return dst, err
	}
	// fsync + checked close: this backup is the caller's rollback of
	// last resort — it must actually be on disk before the caller
	// overwrites src, or a crash could leave BOTH copies truncated.
	if err := syncBackup(out); err != nil {
		_ = out.Close()
		return dst, err
	}
	if err := out.Close(); err != nil {
		return dst, err
	}
	pruneBackups(backupDir, base, MaxBackupsPerFile)
	return dst, nil
}

// syncBackup fsyncs a freshly-written backup file. A package-level
// seam (there is no natural error injection point on *os.File) so
// tests can prove sync failures propagate out of Backup.
var syncBackup = func(f *os.File) error { return f.Sync() }

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
