package conversations

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// resumeSessionPrefix marks tmux sessions ccmux started to resume a
// past conversation.
const resumeSessionPrefix = "c-resume-"

// ResumeSessionName returns the tmux session name ccmux uses when it
// resumes conversation id. The TUI (Conversations Enter, project-menu
// resume rows) and `ccmux resume` both call it, so resuming the same
// conversation from either surface lands on the same session.
//
// The name must be unique per conversation. It used to be the prefix
// plus the ID's first 8 characters, but Codex IDs are UUIDv7, whose
// leading 8 hex digits are a millisecond-timestamp prefix: two
// conversations started within ~65s shared them, and resuming the
// second one attached to (and re-tagged) the first one's session. The
// name keeps that recognizable 8-character prefix — what the UI shows
// as the short ID — and appends 8 hex digits of a SHA-256 of the full
// ID. Characters tmux can't take in a session name are replaced.
func ResumeSessionName(id string) string {
	id = strings.TrimSpace(id)
	short := []rune(id)
	if len(short) > 8 {
		short = short[:8]
	}
	sum := sha256.Sum256([]byte(id))
	return resumeSessionPrefix + sanitizeSessionPart(string(short)) + "-" + hex.EncodeToString(sum[:4])
}

// sanitizeSessionPart keeps ASCII letters, digits, '-' and '_' and
// maps everything else to '-'. tmux rewrites '.' and ':' in session
// names (they're target separators), which would make the name ccmux
// later looks up differ from the one tmux created.
func sanitizeSessionPart(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
