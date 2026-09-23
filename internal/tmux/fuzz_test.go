package tmux

import (
	"strings"
	"testing"
)

// FuzzSessionNameForPath exercises the path → tmux-session-name
// transform with arbitrary filesystem paths. Contract:
//
//  1. Never panics.
//  2. Always returns a non-empty string starting with "c-".
//  3. Never contains the characters tmux uses as session-name
//     terminators in its CLI. Specifically:
//     - `:` is tmux's window/pane separator; `tmux send-keys -t
//     c-foo:0` would route to window 0 of session c-foo, but a
//     session named `c-foo:bar` would break `-t` parsing.
//     - `.` was historically reserved in tmux session targets (the
//     pane component of -t); we replace dots with underscores by
//     design, and the fuzz target pins that invariant.
//  4. Names already in the safe alphabet map to exactly `c-<name>`;
//     a name that had to be rewritten never lands on that plain form
//     (so `my.app` can't take over `my_app`'s session).
//
// Why this matters: session names get used in `tmux -t <name>` args
// across `internal/tmux` and chrome / send-keys / etc. A path with
// shell metacharacters or tmux metacharacters slipping through
// SessionNameForPath would break the daemon's tmux interactions for
// that session.
func FuzzSessionNameForPath(f *testing.F) {
	for _, seed := range []string{
		"foo",
		"/Users/skz/Projects/foo",
		"foo.bar.baz",
		"/a/b/c/",
		"",
		".hidden",
		"a:b",
		"a/b\nc",
		"a\x00b",
		"//",
		strings.Repeat("a", 256),
		"/Users/me/Projects/my.app",
		"/Users/me/Projects/日本",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		got := SessionNameForPath(path)

		if !strings.HasPrefix(got, "c-") {
			t.Fatalf("SessionNameForPath(%q) = %q — must start with `c-`", path, got)
		}
		if got == "c-" {
			// The basename was empty (e.g. "/" or ""). That's fine for
			// the function itself but flag it so a future change that
			// makes this propagate to tmux ends up surfaced.
			return
		}
		if strings.Contains(got, ":") {
			t.Fatalf("SessionNameForPath(%q) = %q contains `:` — tmux uses this as a window/pane separator", path, got)
		}
		if strings.Contains(got, ".") {
			t.Fatalf("SessionNameForPath(%q) = %q contains `.` — should have been replaced with `_`", path, got)
		}
		if strings.IndexFunc(got, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
		}) >= 0 {
			t.Fatalf("SessionNameForPath(%q) = %q has a byte outside [a-zA-Z0-9_-]", path, got)
		}
		// 4. Safe names are unchanged (existing sessions keep
		//    attaching); a lossy rewrite never lands on the name of the
		//    safe directory it sanitized to (`my.app` vs `my_app`).
		base := lastSegment(path)
		plain := "c-" + sanitizeSessionName(base)
		if sanitizeSessionName(base) == base {
			if got != plain {
				t.Fatalf("SessionNameForPath(%q) = %q, want %q for an already-safe name", path, got, plain)
			}
		} else if got == plain {
			t.Fatalf("SessionNameForPath(%q) = %q collides with the safe name %q", path, got, sanitizeSessionName(base))
		}
	})
}
