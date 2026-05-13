package claude

import (
	"testing"
	"time"
)

// FuzzClassify drives the pane-content state classifier with arbitrary
// strings. The daemon's poll loop feeds this `tmux capture-pane` output
// every 2 seconds, so a malformed capture (binary garbage, half-rendered
// frame, terminal escape sequences captured inside the pane) must not
// crash the daemon. Contract:
//
//  1. Never panic — the function reads strings, uses `strings.Contains`
//     and `strings.HasSuffix`, and returns a State string. Any input
//     it consumes via those primitives is safe in principle, but
//     adding unicode pathologies + control bytes to the seed corpus
//     surfaces any future regression that introduces a panic path
//     (e.g. an unchecked slice index in a "small optimization").
//
//  2. The return is always one of the five known State values. If
//     Classify ever returns "" or some new string the dashboard
//     hasn't been taught to render, sessions on the dashboard would
//     get rendered as the empty state with no color and confuse the
//     user. statePriority (over in internal/tui) would also break.
func FuzzClassify(f *testing.F) {
	// Seeds borrowed from the existing TestClassify cases plus a few
	// known-pathological strings the fuzzer will mutate from.
	for _, seed := range []string{
		"",
		" ",
		"recently-active line",
		"╭─────────╮\n│ > write a function │\n╰─────────╯",
		"skz$ \n",
		"\x00\x00\x00",
		"\xff\xfe garbage utf-8",
		"line\nline\nline\n", // many lines, none look like a prompt
		"a\rb",
		"  ",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, pane string) {
		// Vary the staleness so the fuzzer can exercise both the
		// "fresh content → active" and "stale → idle/needs_input"
		// branches. lastChange is now (fresh) every call; the
		// idleThreshold below picks the branch.
		for _, idle := range []time.Duration{0, 1 * time.Second, 10 * time.Second} {
			got := Classify(pane, time.Now(), idle)
			switch got {
			case StateUnknown, StateActive, StateIdle, StateNeedsInput, StateError:
				// canonical State — good
			default:
				t.Fatalf("Classify(%q, idle=%v) = %q — not one of the five known states", pane, idle, got)
			}
		}
	})
}
