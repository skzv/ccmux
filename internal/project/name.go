package project

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidateName checks a new project's name before it is joined onto
// the projects root: it must be a single, non-hidden path segment, so
// "../x", "a/b" or ".secret" can't create directories outside (or
// hidden inside) the root. Shared by the TUI form, `ccmux new`, and the
// daemon's POST /v1/projects.
//
// Control characters (C0, DEL, C1) and invalid UTF-8 are refused too:
// the name becomes a directory that every later listing prints, and an
// ESC or a raw 0x9B (8-bit CSI) in it made `ccmux list` and friends
// emit live terminal escape sequences.
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
	case strings.ContainsFunc(name, unicode.IsControl):
		return errors.New("name must not contain control characters")
	case !utf8.ValidString(name):
		return errors.New("name must be valid UTF-8")
	}
	return nil
}
