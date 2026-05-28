## Context

Conversations is the cross-agent transcript browser — Claude (`~/.claude/projects/*/`), Codex (`~/.codex/`), and Antigravity (`~/.gemini/antigravity-cli/`). The screen is sectioned by agent with `tab` / `shift+tab` cycling, and within each section it's a flat list of logical conversations sorted by recency.

PR #114 migrated selection to `components.RenderListRow`, dropped the inline footer hint, and added `conversationsModel.HelpBarProps` (with the dynamic `H headless: hidden/shown` label that reads the current toggle state). The existing golden snapshots at width 119 to avoid the `Last active 2026-05-26 21:50` timestamp drift in the wide-mode detail pane.

The visual gap: every agent label and every section heading reads identically (mauve emphasis when active, muted when not). The Dashboard's Usage panel established a per-agent palette (Claude=mauve, Codex=sky, Antigravity=peach); Conversations is the natural next surface to apply it — both the section nav row at the top of the screen and the per-row agent label column.

The data-shape gap: a logical conversation can be scattered across more than one JSONL. Claude may put parent-session work in `<uuid>.jsonl` and delegated work under `<uuid>/subagents/*.jsonl`; Codex rollout files can share the same rollout UUID. The user-facing row should be the logical conversation, not each storage fragment.

## Goals / Non-Goals

**Goals:**

- Make the active agent section visually identifiable by its accent colour, not just by a glyph.
- Carry the same colour to per-row agent labels so a row's agent reads from across the screen.
- Eliminate the timestamp-drift flake on the wide golden by moving the absolute timestamp behind a modal.
- Add a transcript preview modal (`p`) so the user can read more of a conversation without leaving the tab.
- Merge known scattered transcript fragments in memory so the list, detail pane, preview modal, and delete flow operate on one logical conversation row.

**Non-Goals:**

- Physical transcript migration. Agent-owned JSONL/PB files stay exactly where the agent wrote them.
- Resume semantics changes. `enter` still resumes by the logical conversation/session ID.
- Cross-agent cost normalisation. That's the Codex cost estimator follow-up.

## Decisions

### Per-agent palette (single source of truth)

**Decision:** Define agent → colour as a single mapping consumed by the Dashboard's Usage panel, the Agents sub-tab row (`redesign-tui-agents`), and the Conversations section nav + agent labels. Claude=mauve, Codex=sky, Antigravity=peach, Cursor=teal.

**Rationale:** Today Dashboard's `agentSectionHeading(st, id, text)` carries this mapping inline. Cross-tab consistency means the mapping should live in one place — probably a small `agentAccent(styles.Styles, agent.ID) lipgloss.Style` exported from `internal/tui/styles/` or kept on the Styles aggregate.

**Alternatives:**

- _Per-screen rebinding._ Already shown to drift (Dashboard had inline mauve; if Conversations chose differently, they'd diverge).

### Detail pane → preview modal

**Decision:** Trim the wide-mode detail pane to ID + project + last-active relative timestamp (e.g., `5h ago`). The absolute timestamp + the full preview move into a `p` modal.

**Rationale:** The absolute timestamp is the timestamp-drift source for the existing golden. Moving it behind a modal makes the default view stable AND surfaces a place to render more transcript context (last ~30 messages via Glamour).

**Alternatives:**

- _Inject a clock seed for tests._ Works, but doesn't change the user-facing layout. The trim has both a stability + readability benefit.

### Logical transcript merge

**Decision:** Merge transcript fragments in memory, preserving `Conversation.Path` as the primary/resumable transcript path and exposing all contributing fragments through `Conversation.Paths`. Claude parent `<uuid>.jsonl` plus `<uuid>/subagents/*.jsonl` fragments become one row keyed by `<uuid>`. Codex rollout files with the same UUID become one row keyed by that UUID.

**Rationale:** The Conversations tab is a resume/read/delete surface for user-visible sessions. Showing one row per storage fragment makes delegated Claude work look like unrelated sessions and splits preview/count data. Keeping the merge in ccmux's read model avoids mutating agent-owned transcript stores.

**Delete semantics:** A merged row deletes every validated fragment for that logical conversation. The guard validates all fragment paths before removing any file so a corrupted `Conversation.Paths` value cannot cause partial arbitrary deletion.

**Alternatives:**

- _Physically concatenate JSONL files._ Rejected because it mutates agent-owned data and could break agent resume/indexing.
- _Show parent and subagents separately._ Rejected because it preserves implementation storage details in the user's conversation browser.

### Armed-for-delete chip

**Decision:** When `c.ID == m.pendingDelete`, replace the row's preview text with the `[delete? x to confirm · esc]` chip at the trailing edge. The agent label + timestamp stay in place.

**Rationale:** Today the entire row's preview is replaced by a status-error string. The chip approach keeps the row's identifying columns (agent, time) visible so the user can confirm they're targeting the right row.

### Compressed detail pane

**Decision:** The detail pane is a flat, compressed layout — no sub-section headings. From top to bottom: agent name (accent-coloured), tilde-collapsed project path, a small "label value" column (`last active`, `messages`, conditional `mode`), a "First prompt" recap of `Conversation.Preview`, and the action keybinds.

**Rationale:** An earlier iteration grouped fields under `Identity` and `Activity` subtitles. In practice, `Identity` carried two fields (UUID + path) where the UUID is debugging-only — users only want the path. Removing the subtitles and the UUID makes the pane scan top-to-bottom without visual clutter. The `messages` row is a small "thread length" signal that walks the JSONL once per cursor change (memoized after the first walk).

## Risks / Trade-offs

- **[Risk] Per-agent colour saturation on a Codex-heavy user's view.** A user with mostly Codex conversations sees a lot of sky. → Mitigation: only the section heading and the agent label column carry the colour; the row's preview and timestamp stay default-foreground.
- **[Trade-off] Modal-only timestamp.** Power users may prefer always-visible absolute times. → Justified: the relative `5h ago` is enough for the scan, and the modal is one keystroke away.

## Migration Plan

1. Extract the agent-accent mapping to a shared helper consumed by Dashboard + Conversations.
2. Migrate `renderAgentNav` + `renderConversationRowContent` to use the helper.
3. Implement the chip-based armed-for-delete state.
4. Merge known scattered Claude/Codex transcript fragments in the conversation data layer.
5. Trim the detail pane; regenerate the 119×40 golden.
6. Add the `p` modal + a 120×40 golden of the modal-open state.

Rollback: revert. No data migrations.

## Open Questions

- Should the `p` modal's Glamour rendering use the Notes-tab Glamour theme (which itself is being tuned in `redesign-tui-notes`)? **Tentative:** Yes — same TUI, same Glamour theme. Either change can land first; the second adopts the first's theme.
