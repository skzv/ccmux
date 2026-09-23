package project

import (
	"errors"
	"strings"
)

// ValidateName checks a new project's name before it is joined onto
// the projects root: it must be a single, non-hidden path segment, so
// "../x", "a/b" or ".secret" can't create directories outside (or
// hidden inside) the root. Shared by the TUI form, `ccmux new`, and the
// daemon's POST /v1/projects.
func ValidateName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return errors.New("name is required")
	case strings.ContainsAny(name, `/\`):
		return errors.New("name must be a single directory name (no / or \\)")
	case strings.HasPrefix(name, "."):
		return errors.New("name must not start with a dot")
	case strings.ContainsRune(name, 0):
		return errors.New("name contains a NUL byte")
	}
	return nil
}
