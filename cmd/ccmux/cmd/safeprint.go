package cmd

import (
	"strings"

	"github.com/skzv/ccmux/internal/termsafe"
)

// Human-readable CLI output carries text ccmux didn't write: note file
// names and snippets, project and session names, conversation IDs and
// working directories, host names, model names, daemon error bodies. A
// name like `n\x1b]52;c;SGVsbG8=\x07x.md` printed raw writes the
// terminal's clipboard (OSC 52); a CSI in a session name can repaint the
// screen. Everything externally sourced goes through these helpers
// before it is printed. JSON output is left alone: the encoder already
// escapes control characters.

// safeText strips terminal control sequences from multi-line text (a
// note body, an error message). Newlines and tabs survive.
func safeText(s string) string { return termsafe.String(s) }

// safeField is safeText for one cell of a single-line or tabular row:
// a newline or tab (which termsafe keeps) would split the row or shift
// its columns, so each becomes a space.
func safeField(s string) string {
	s = termsafe.String(s)
	if !strings.ContainsAny(s, "\n\t") {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, s)
}

// shellWord renders s as one shell word for a command the user may
// copy-paste: sanitized, and single-quoted unless it's made only of
// characters no shell treats specially.
func shellWord(s string) string {
	s = safeField(s)
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-/@%+=:,") == "" {
		return s
	}
	return shellQuote(s)
}

// ErrorMessage renders err for the terminal (main prints it after
// "ccmux:"). Errors quote daemon responses, file names and tmux output,
// so they are sanitized like any other external text.
func ErrorMessage(err error) string { return safeText(err.Error()) }
