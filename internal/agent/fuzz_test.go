package agent

import (
	"testing"
)

// FuzzParseID exercises the agent-id parser with arbitrary strings.
// The contract this fuzz target enforces:
//
//  1. ParseID never panics, regardless of input.
//  2. ok=true implies the returned ID is one of the canonical values
//     (claude / codex / antigravity / cursor). Anything else would mean
//     ParseID is silently coining new agents that nothing else in
//     the codebase knows how to handle. The legacy "gemini" input is
//     allowed via the back-compat alias and resolves to antigravity.
//  3. ok=false implies the returned ID is the empty string. Callers
//     rely on this: a bogus user-typed id should never look like a
//     "successful" parse with a partial result.
//
// Seed corpus mirrors the cases the existing TestParseID covers,
// plus a couple of unicode + control-char shapes the fuzzer will
// expand from. Failing seeds get auto-archived under
// internal/agent/testdata/fuzz/FuzzParseID/ per Go's convention and
// turn into regression tests on the next `go test` run.
func FuzzParseID(f *testing.F) {
	for _, seed := range []string{
		"claude",
		"CODEX",
		"  antigravity  ",
		"AnTiGrAvItY",
		"gemini", // back-compat alias
		"GeMiNi",
		"cursor",
		"pi",
		"grok",
		"",
		"   ",
		"opusplan",      // close to a Claude alias but not a valid id
		"claude-3",      // looks-plausible variant
		"\x00",          // NUL byte
		"\xff\xfe",      // invalid utf-8
		"こんにちは",         // unicode
		"a/b",           // path separator
		"a\nb",          // embedded newline
		"a b c d e f g", // multi-token
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		id, ok := ParseID(s)
		if ok {
			switch id {
			case IDClaude, IDCodex, IDAntigravity, IDCursor, IDPi, IDGrok:
				// canonical id — good
			default:
				t.Fatalf("ParseID(%q) returned ok=true but id=%q is not in {claude,codex,antigravity,cursor,pi,grok}", s, id)
			}
		} else {
			if id != "" {
				t.Fatalf("ParseID(%q) returned ok=false with non-empty id=%q — callers rely on the empty-on-failure invariant", s, id)
			}
		}
	})
}
