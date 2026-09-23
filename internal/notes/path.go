package notes

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
)

// ErrOutsideVault is returned for a note path that is absolute or
// climbs out of the project with "..".
var ErrOutsideVault = errors.New("note path must stay inside the project")

// CleanRel normalises a slash-separated, project-relative note path and
// rejects one that would resolve outside the project. Every entry point
// that turns user or network input into a note path (Vault.Read, the
// TUI's new-note form, the daemon's /v1/notes) goes through it.
func CleanRel(rel string) (string, error) {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	if rel == "" {
		return "", errors.New("note path is empty")
	}
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" || strings.ContainsRune(rel, 0) {
		return "", ErrOutsideVault
	}
	cleaned := path.Clean(rel)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrOutsideVault
	}
	return cleaned, nil
}
