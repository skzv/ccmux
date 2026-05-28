## Why

PR #114 (`redesign-tui-charm`) added per-screen `HelpBarProps` to the Agents tab (`tab next agent`, `h/l switch`, `e edit`) but the screen itself wasn't touched. Agents is a sub-tabbed surface — `[◆ Claude] Codex Antigravity Cursor` — that delegates to four sub-models (`claudeModel`, `codexConfigModel`, `antigravityConfigModel`, an empty Cursor view).

Two pain points:

- The sub-tab row renders all four labels in muted text with the active one in `s.Emphasis` (lavender bold). PR #114 introduced per-agent color coding on the Dashboard's Usage panel (Claude=mauve, Codex=sky, Antigravity=peach). The Agents sub-tab row is the natural surface to inherit it.
- The Cursor sub-screen is empty because `internal/agent/cursor.go` says "transcript parsing is not wired". But `~/.cursor/ai-tracking/ai-code-tracking.db` exists and is readable (an SQLite database with `ai_code_hashes`, `scored_commits`, `conversation_summaries` tables — surveyed during the redesign-tui-charm investigation). This change wires it up: a new `internal/cursorusage` package + a populated Cursor sub-screen.

The Claude sub-screen already has rich settings (model picker, effort picker, alwaysThinking, yolo, c/j editor shortcuts). Codex and Antigravity sub-screens are skeleton today.

## What Changes

- **Per-agent colour coding on the sub-tab row**: `agentsModel.renderSubtabs` switches from `s.Emphasis` for the active tab to the agent's accent colour. Inactive tabs stay muted. The `◆` glyph keeps marking the active sub-tab.
- **HelpBar refinement** (per-sub-tab): surface per-sub-tab keys (e.g., when Claude sub-tab is active, surface `m model · e effort · a alwaysThinking · y yolo · c CLAUDE.md · j settings.json`). Drop entries that only apply to other sub-tabs from the wrong-sub-tab's HelpBar.
- **`internal/cursorusage` package**: new package that opens `~/.cursor/ai-tracking/ai-code-tracking.db` read-only and surfaces:
  - Conversation count (DISTINCT `conversationId` from `ai_code_hashes`).
  - Most-used models (DISTINCT `model` aggregated by request count).
  - AI lines added in the last 7 days (sum of `tabLinesAdded + composerLinesAdded` from `scored_commits`).
  - Latest activity timestamp.
- **Cursor sub-screen population**: today the Agents View on the Cursor sub-tab renders `Cursor settings are managed by Cursor CLI.` Replace with a populated screen rendering the cursorusage summary in the same Claude / Codex sub-section style.
- **Fix the stale `TranscriptsRoot` path**: `internal/agent/cursor.go:27` claims `~/.cursor/sessions/` but the real layout is `~/.cursor/chats/<hash>/<uuid>/`. Update the path.
- **Sub-section grouping on Claude sub-screen**: group model/effort/flags/links into `Defaults` (model, effort, alwaysThinking, yolo) and `Config files` (CLAUDE.md, settings.json) with the design-system 2-cell indent step.
- **Per-screen golden**: add `agents_claude.txt`, `agents_cursor.txt` (data populated), `agents_cursor_empty.txt` (no `~/.cursor` present).
- **`bubbles/spinner` for the SQLite read**: show a spinner while cursorusage is loading.

**Non-goals:**

- No write access to `~/.cursor` databases. Read-only.
- No Cursor-CLI session launching changes.
- No Codex cost estimator (separate cross-cutting change).
- No model-picker UI changes on Codex / Antigravity sub-screens (they stay skeleton).

## Capabilities

### Modified Capabilities

- `tui-design-system`: adds Agents-specific scenarios for per-agent colour coding on sub-tab rows. Adds a requirement that the per-agent palette (Claude=mauve, Codex=sky, Antigravity=peach, Cursor=teal) is the single source of truth — applied consistently on the Dashboard's Usage panel, the Conversations section nav, the Agents sub-tab row, and any future per-agent surface.

### New Capabilities

- `cursor-usage`: read-only access to Cursor's local `ai-tracking.db` SQLite for conversation count, model usage, and AI-authored line counts. Surfaces as a `cursorusage.Summary` struct; consumed by the Agents Cursor sub-screen.

## Impact

- **Affected code:** `internal/tui/agents.go` (sub-tab colour, sub-section grouping, per-sub-tab HelpBar), `internal/tui/claudeconfig.go` (sub-section grouping on Claude sub-screen), `internal/tui/app.go` (route the Cursor sub-tab to a real render), `internal/agent/cursor.go` (fix TranscriptsRoot path).
- **New package**: `internal/cursorusage/` — SQLite read of `~/.cursor/ai-tracking/ai-code-tracking.db`, ~150 lines + tests.
- **Tests:** new `cursorusage_test.go` against a fixture SQLite file; one new test for per-agent colour mapping; one new golden per sub-tab.
- **Dependencies:** new dep on `modernc.org/sqlite` (pure-Go SQLite — no CGO).
- **User-visible:** Cursor sub-tab no longer empty for Cursor users; per-agent colours appear on the sub-tab row; Claude sub-screen reads as two grouped sections.
- **CLI:** no changes.
