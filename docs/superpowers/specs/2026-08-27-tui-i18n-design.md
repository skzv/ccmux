# TUI i18n / 汉化 + 语言切换 — Design

Date: 2026-08-27
Status: approved (brainstorming)

## Problem

ccmux's Bubble Tea TUI hardcodes English strings across ~20 screen files (`internal/tui/` ≈ 4,478 string literals total, of which the user-visible subset is roughly 800–1,500). There is no i18n infrastructure anywhere in the repo: no i18n dependency in `go.mod`, no language field in `internal/config`, and the only "locale" logic is the UTF-8 locale forced on `tmux` subprocesses (unrelated to UI language).

Goal: let the TUI display Simplified Chinese, switchable at runtime, without breaking existing English behavior.

## Scope decisions (locked)

1. **Layer scope: TUI only** (`internal/tui`). CLI output (`cmd/ccmux/cmd`), daemon error messages (`cmd/ccmuxd`, `internal/daemon`), tmux status bar chrome (`internal/tmuxchrome`), and logs stay English.
2. **Switch mechanism: config + system fallback.** New `config.lang` field; empty → follow `$LANG`/`$LC_ALL` (`zh*` → Chinese, else English); explicit value overrides. Settings screen gets a Language row.
3. **Coverage: full, one pass.** All user-visible strings in all screens plus shared components (keybindings, toasts, confirmation dialogs, tab names). Missing translations fall back to English by design — a miss can never break rendering.
4. **Language set: English (default) + Simplified Chinese.** No plural system (Chinese has none; English strings are preserved verbatim as the default).
5. **Mechanism: self-built lightweight `internal/i18n`** (zero new deps), English-phrase-as-key, and a sync lint test (`internal/i18n/sync_test.go`) whose **inline `go/ast` extractor** walks `internal/tui/...` collecting every `tr("…")` key and asserts each has a `zh.toml` entry (the repo's "test-enforced invariant" style, cf. `FUZZ_TARGETS`, `agent.All()/ByID()/ParseID()`, `styles_lint_test.go`). Extractor lives inside the test (not a separate `go:generate` command) so the check can never go stale.

## Architecture

### `internal/i18n` package (zero dependencies)

- `T(key string) string` — lookup in the current language's translation table; returns the key verbatim when absent (English key == fallback value).
- `SetLanguage(lang string)` / `Current() Lang` — package-level current language; test-injectable and restorable.
- `Resolve(lang string, env func(string) string) Lang` — pure function: a non-empty config `lang` wins; else probe `$LC_ALL` then `$LANG` (`zh*` → Chinese, anything else → English). Pure so it's table-testable.
- Translation files embedded via `go:embed internal/i18n/locales/zh.toml`, parsed once at startup. English needs no file — the key is the English text.
- Parse/embed failures: log a warning at startup and fall back to English (key verbatim). Never crash.

### TUI integration

- `internal/tui` gains a package-level `tr(key string) string` thin wrapper over `i18n.T`. Package-level (not per-model field) because ~20 screen files change and it keeps the diff minimal; tests can switch the whole package's language at once.
- Call shape: `tr("Attach")`, or `fmt.Sprintf(tr("Attach to %s"), name)`. `tr` takes exactly one literal key argument — the sync extractor collects only that; templates are assembled with `fmt.Sprintf` so a missing translation falls back to a coherent English template, not a broken one.
- **Replace:** screen titles, tab names, form labels, buttons, keybinding hints, toasts, confirmation dialogs, empty states, error hints, status-bar labels.
- **Do not replace:** session/project/file names (user data); agent proper nouns (Claude, Codex, Cursor, …); the `c-` session-name prefix; ANSI escapes / internal identifiers / key names / `_test` helpers; `internal/agent`'s `InitialPrompt` (instruction text sent to agents, not UI).
- **Settings screen:** add a "Language / 语言" row (huh select, en/zh). On select: write `config.lang`, call `i18n.SetLanguage`, and trigger a re-render. Hot-switch is free — every `View()` re-reads through `tr()`.
- **Config:** add `Lang string` (`toml:"lang,omitempty"`) to `internal/config`. At App init: `i18n.SetLanguage(cfg.Lang)` (empty resolves via `$LANG`).

### Double-width characters / alignment

- Main render paths are already safe: `lipgloss.Width` measures display columns via go-runewidth, and layout internally uses rivo/uniseg. Dashboard's truncation budget (`inner - lipgloss.Width(prefix) - lipgloss.Width(suffix)`) is width-aware.
- **Risk surface** (checked per-screen during implementation): manual `strings.Repeat(" ", n)` alignment, `.PaddingLeft(...)`, fixed-width space-padding used to build tables — Chinese labels have different display widths and will misalign. Strategy: prefer lipgloss width-aware layout (`style.Width(w)`, `lipgloss.JoinHorizontal`); for one-off tables add a display-width-aware pad helper if needed.
- tmux status bar chrome is out of scope (not translated; mostly user data + fixed English labels).

## Testing

- **`internal/i18n` unit tests:** `Resolve` table-driven over `$LANG` values + config override + empty; `T()` fallback semantics (present → Chinese, absent → English verbatim); parse-failure → English.
- **Sync lint test (the key invariant):** a `go:generate` tool walks `internal/tui` with `go/ast` collecting every `tr("…")` literal argument → key set; `internal/i18n/sync_test.go` asserts code keys ⊆ zh.toml keys. A new/changed English string with no translation turns CI red. AST (not regex) so `tr("…")` appearing in comments is not mis-collected.
- **Existing-test isolation:** every TUI test pins `i18n.SetLanguage("en")` with `t.Cleanup` restore, so width-sweep anchors ("Sessions", "Hello."), golden snapshots, and e2e English anchors all pass unchanged.
- **zh verification:** correctness of Chinese is carried by the sync test + manual spot-check. No per-screen zh golden files in this pass (two snapshots per screen doubles maintenance for low value). Optionally one e2e smoke: with `lang="zh"`, the Dashboard shows "会话".
- **Alignment regression:** the existing `width_test.go` sweep (`assertNoOverflow`) runs in English; add a zh-mode sweep that renders the same screens with `SetLanguage("zh")` and asserts no line overflows its width — the net that catches manual-padding misalignment introduced by double-width text.

## Implementation order (per-screen commits)

1. `internal/i18n` package + `Resolve` + unit tests + `zh.toml` skeleton
2. `internal/tui` + `internal/tui/components` `tr()` thin wrappers + `sync_test.go` (inline AST extractor)
3. `config.lang` + `App.New` hook + Settings language row (hot-switch) + related tests
4. English isolation of existing tests (`TestMain` pins `en`; `withLang(t, …)` helper for zh tests)
5. Screen-by-screen replacement — **each screen's commit ships both the `tr()` edits and that screen's `zh.toml` entries**, so the sync test stays green throughout: components/shared chrome (help/keys) → Dashboard → Sessions → Projects → Notes → Conversations → Agents → Network → Settings/misc (confirm/toast/menus/wizards)
6. Alignment/width code review (manual padding audit) + zh width-sweep test
7. Final verification: `go test ./...` + `make lint`; e2e harness pinned to English (`LANG` or `lang="en"` config) so e2e anchors stay stable

Translation is filled per-screen inside task 5 rather than as a final bulk pass — the sync test error list is the machine-readable "which keys still need zh" checklist.

## Error handling

- Translation file parse failure → log warning + fall back to English (key verbatim). Never crash.
- Invalid `config.lang` value → log + fall back to `$LANG` resolution.
- Missing translation → English key. **Designed behavior, not an error.**

## Out of scope (explicit)

- CLI output, daemon errors/logs, tmux status bar, `ccmux-mcp` messages.
- Traditional Chinese, other languages, RTL.
- Pluralization.
- Agent `InitialPrompt` text.
- Per-screen zh golden snapshots.
