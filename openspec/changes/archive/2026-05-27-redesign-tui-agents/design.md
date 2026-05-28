## Context

The Agents tab is a sub-tabbed surface: `[◆ Claude] Codex Antigravity Cursor`. `agentsModel.Update` handles `tab` / `shift+tab` / `h` / `l` to cycle sub-tabs, then delegates `Update` + `View` to the active sub-model (`claudeModel`, `codexConfigModel`, `antigravityConfigModel`, an inline string for Cursor).

PR #114 added `agentsModel.HelpBarProps` (`tab next agent`, `h/l switch`, `e edit`) but didn't touch the sub-tab row's rendering, the per-sub-tab key handling, or the Cursor sub-screen's placeholder.

Three things converge in this change:

1. The Dashboard's Usage panel established a per-agent colour palette (Claude=mauve, Codex=sky, Antigravity=peach). The Agents sub-tab row is the natural place to apply it.
2. The Cursor sub-screen is empty because `internal/agent/cursor.go` claims `~/.cursor/sessions/` (stale path) and notes "transcript parsing is not wired". The real layout — surveyed during PR #114's investigation — is `~/.cursor/chats/<hash>/<uuid>/store.db` per-conversation + `~/.cursor/ai-tracking/ai-code-tracking.db` aggregate. The aggregate has `ai_code_hashes` (model + timestamp per request), `scored_commits` (per-commit AI-line counts), `conversation_summaries` (titles). This is rich, structured, read-only-friendly data.
3. The HelpBar is single-set for the whole Agents tab, but per-sub-tab the relevant keys differ (Claude has m/e/a/y/c/j; Codex/Antigravity have a subset; Cursor has none today). A per-sub-tab HelpBar would advertise the right keys at the right time.

## Goals / Non-Goals

**Goals:**

- Wire Cursor usage so the Cursor sub-tab stops being a placeholder for users who actively use Cursor.
- Establish the per-agent colour palette as the design-system's single source of truth.
- Make the Agents HelpBar context-aware to the active sub-tab.
- Group the rich Claude sub-screen into labeled sections.

**Non-Goals:**

- Modifying `cursor-agent` session-launch semantics. `agent.Cursor.LaunchCommand` and the resume flow stay as they are.
- Codex cost estimator. Separate cross-cutting change.
- A new sub-tab. Cursor stays as the fourth (last) sub-tab.

## Decisions

### Per-agent palette as shared helper

**Decision:** Extract the agent-accent mapping (currently inline in `dashboard.go`'s `agentSectionHeading`) into a shared helper, e.g., `styles.AgentAccent(id agent.ID) lipgloss.Style`. Consumed by:

- Dashboard's Usage panel (existing).
- Agents sub-tab row (this change).
- Conversations' section nav + agent labels (`redesign-tui-conversations`).
- Any future per-agent surface.

**Rationale:** Single source of truth. The Conversations change references the same helper; cross-tab consistency comes from the import, not from copy-pasted colour choices.

### Cursor SQLite reader

**Decision:** New `internal/cursorusage` package. Opens `~/.cursor/ai-tracking/ai-code-tracking.db` read-only via `modernc.org/sqlite` (pure Go, no CGO — keeps cross-compile clean). Surfaces a `cursorusage.Summary` with:

- `Conversations int` — count from `SELECT COUNT(DISTINCT conversationId) FROM ai_code_hashes`.
- `Models []string` — `SELECT DISTINCT model FROM ai_code_hashes ORDER BY COUNT(*) DESC LIMIT 5`.
- `AILinesLast7d int` — `SELECT SUM(tabLinesAdded + composerLinesAdded) FROM scored_commits WHERE scoredAt >= ?`.
- `LastActivity time.Time` — `SELECT MAX(timestamp) FROM ai_code_hashes`.

**Rationale:** `modernc.org/sqlite` is the standard pure-Go SQLite driver in the Go ecosystem; ccmux already cross-compiles for darwin/linux/windows × amd64/arm64 and pure-Go SQLite avoids CGO + libtool noise on each.

**Alternatives:**

- _Shell out to `sqlite3` CLI._ Adds a runtime dependency users have to install.
- _Use CGO-based `mattn/go-sqlite3`._ Faster but breaks the cross-compile job (`GOOS=windows` etc.) without each runner having a C toolchain.

### Per-sub-tab HelpBar

**Decision:** `agentsModel.HelpBarProps(width)` switches on `m.active` and returns the keys relevant to that sub-tab:

- Common everywhere: `? help · q quit · tab next · 1-7 screens`.
- Claude sub-tab: + `m model · e effort · a always · y yolo · c CLAUDE.md · j settings.json`.
- Codex / Antigravity: + `y yolo · e edit`.
- Cursor: + `(read-only)`.

**Rationale:** The hint line should match what the active surface actually does.

### Claude sub-screen sub-sections

**Decision:** Group `claudeModel.View()` content into `Defaults` (model, effort, alwaysThinking, yolo) and `Config files` (CLAUDE.md path, settings.json path) with the design-system 2-cell indent.

**Rationale:** Today the rows compete; grouping makes the categories explicit.

### Stale TranscriptsRoot

**Decision:** `internal/agent/cursor.go:27` updates `TranscriptsRoot` to return `~/.cursor/chats/` (the real layout). Anything that walks Cursor transcripts in the future (cross-agent conversation list, future cost estimator) uses the right path.

**Rationale:** Bug fix surfaced during the redesign-tui-charm investigation; ride along with this change since it touches the surrounding code.

## Risks / Trade-offs

- **[Risk] SQLite open at every refresh.** → Mitigation: cache the `cursorusage.Summary` for 30s; the open + queries are sub-second on a typical DB, but no need to do it on every render tick.
- **[Risk] Users without Cursor installed see an empty SQLite open error.** → Mitigation: `cursorusage.Open` returns a sentinel `ErrNotInstalled` when the DB path doesn't exist; the sub-screen renders a "Cursor not detected — install: `cursor` from cursor.com" placeholder.
- **[Trade-off] modernc.org/sqlite is large** (~5 MB binary bloat). → Justified: ccmux is one binary that ships everywhere; the user-visible feature is worth it.

## Migration Plan

1. Extract `styles.AgentAccent` helper. Wire Dashboard's existing usage to it. No visual change yet.
2. Update `agentsModel.renderSubtabs` to use the helper. New golden capture.
3. Implement `internal/cursorusage` against a fixture DB in `testdata/`.
4. Populate Cursor sub-screen with the summary.
5. Per-sub-tab HelpBar switching.
6. Claude sub-screen sub-sections.
7. Fix the TranscriptsRoot path.

Rollback: revert. The cursorusage package is read-only; no Cursor state changes.

## Open Questions

- Should the Cursor sub-screen also list recent conversations (titles from `conversation_summaries`)? **Tentative:** Yes, top 5 by recency — gives the screen the same shape as the rich Claude sub-screen.
- Should the Conversations tab also list Cursor conversations (since we now have the data)? **Tentative:** Out of scope here; that's an extension of `internal/conversations` and belongs in a separate change.
