# Golden snapshots — `internal/tui/testdata/golden/`

Each file in this directory is a frozen TUI render captured at a
canonical terminal size (120 columns by 40 rows for primary
navigation screens). Tests under `internal/tui/` compare a fresh
render against the matching file; a mismatch fails CI.

This is the design-system's visual regression net. Without it,
incidental style drift (a stray padding change, a palette tweak,
a header refactor) can ship without anyone noticing — and over time
the screens slide back toward the busy, ad-hoc look the redesign
was meant to fix.

## When to regenerate

After a deliberate visual change (a token tweak, a component refactor,
a new chip), regenerate the affected goldens and review the diff
before committing them.

```sh
CCMUX_UPDATE_GOLDEN=1 go test ./internal/tui/...
git diff internal/tui/testdata/golden/
```

The diff is the visual change you're shipping. Read it. If anything
in there is unintentional, the diff is showing you a bug.

## How the comparison works

`goldenAssert(t, relPath, got)` (in `internal/tui/golden_test.go`):

- Without `CCMUX_UPDATE_GOLDEN`: reads `testdata/golden/<relPath>`,
  fails the test if it differs from `got`.
- With `CCMUX_UPDATE_GOLDEN=1`: rewrites `testdata/golden/<relPath>`
  with `got` and passes.

Snapshots are stored raw — ANSI escapes included — so they capture
exactly the bytes a terminal would render.

## Determinism

Tests MUST inject deterministic state before snapshotting:

- Clock: the dashboard reads `m.clock()` which returns `m.now` when
  set; tests call `dashboard.SetNow(fixedTime)` before rendering.
- Session timestamps: set `LastChange` / `Created` to a value far
  enough from "now" that `humanDuration` rounds to a stable bucket
  (e.g., `time.Now().Add(-3*time.Minute)` rounds to `"3m"` for any
  test slower than 60 seconds — anything faster than that is fine).
- External data: leave `usage` and `ccusage` nil unless the test
  is specifically pinning their rendering.

Adding a new golden? Document any non-obvious determinism setup in
the test's own comment so the next person doesn't have to figure it
out from a CI failure.
