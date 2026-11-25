// Package configfile is a tiny shared helper for the three agent-config
// packages (claudeconfig, codexconfig, antigravityconfig) so they all
// write atomically and rotate backups identically. The packages
// themselves still own their typed Settings round-trip — this is just
// the "write file, keep N backups" plumbing.
package configfile

import (
	"errors"
	"fmt"
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
	dst = resolveSymlinkTarget(dst)
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

// resolveSymlinkTarget resolves dst through any symlink chain — including
// dangling symlinks (e.g. a dotfile manager's link whose target has not
// been created yet) — so os.Rename writes to the link's target rather
// than replacing the symlink itself with a regular file.
func resolveSymlinkTarget(dst string) string {
	if resolved, err := filepath.EvalSymlinks(dst); err == nil {
		return resolved
	}
	cur := dst
	for range 40 {
		fi, err := os.Lstat(cur)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			break
		}
		target, err := os.Readlink(cur)
		if err != nil {
			break
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(cur), target)
		}
		cur = filepath.Clean(target)
	}
	// Resolve any existing symlinked parent directory of the final target.
	if parent, err := filepath.EvalSymlinks(filepath.Dir(cur)); err == nil {
		return filepath.Join(parent, filepath.Base(cur))
	}
	return cur
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
	base := filepath.Base(src)
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	// 0600: backups are copies of files that can hold API keys, and
	// they outlive any later chmod of the original. Use UTC nanosecond
	// timestamps + O_EXCL so back-to-back saves within 1ms or across a
	// DST fallback never clobber an earlier backup.
	var (
		dst string
		out *os.File
	)
	ts := time.Now().UTC().Format("20060102-150405.000000000Z")
	for attempt := 0; attempt < 100; attempt++ {
		name := base + "." + ts
		if attempt > 0 {
			name = fmt.Sprintf("%s.%s.%d", base, ts, attempt)
		}
		cand := filepath.Join(backupDir, name)
		f, oerr := os.OpenFile(cand, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if oerr == nil {
			dst, out = cand, f
			break
		}
		if !errors.Is(oerr, os.ErrExist) {
			return "", oerr
		}
	}
	if out == nil {
		return "", fmt.Errorf("create unique backup file in %q: exhausted retries", backupDir)
	}
	cleanup := func() {
		_ = out.Close()
		_ = os.Remove(dst)
	}
	if _, err := io.Copy(out, in); err != nil {
		cleanup()
		return "", err
	}
	// fsync + checked close: this backup is the caller's rollback of
	// last resort — it must actually be on disk before the caller
	// overwrites src, or a crash could leave BOTH copies truncated.
	if err := syncBackup(out); err != nil {
		cleanup()
		return "", err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return "", err
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
		if suffix, ok := strings.CutPrefix(name, prefix); ok && isBackupSuffix(suffix) {
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

// isBackupSuffix reports whether suffix looks like a numeric/timestamp
// backup suffix (e.g. "20260102-150405.000", "20260102-150405.000000000Z",
// "000001") rather than an unrelated filename sharing a prefix (e.g.
// base "config" must not match "config.toml.20260102-...").
func isBackupSuffix(suffix string) bool {
	if len(suffix) == 0 || suffix[0] < '0' || suffix[0] > '9' {
		return false
	}
	for i := 0; i < len(suffix); i++ {
		c := suffix[i]
		if !((c >= '0' && c <= '9') || c == '-' || c == '.' || c == 'Z') {
			return false
		}
	}
	return true
}
