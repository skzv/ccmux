## 1. Per-agent palette as shared helper

- [x] 1.1 Add `styles.AgentAccent(id agent.ID) lipgloss.Style` (or `Styles.AgentAccent(id)` method). Mapping: Claude=Mauve, Codex=Sky, Antigravity=Peach, Cursor=Teal.
- [x] 1.2 Migrate `dashboard.go`'s `agentSectionHeading` to consume the helper (single source of truth).
- [x] 1.3 Unit test covering all four IDs + an unknown ID default.

## 2. Conversations agent-accent wiring

- [x] 2.1 Migrate `renderAgentNav` to colour the active section heading via the helper.
- [x] 2.2 Migrate `renderConversationRowContent` to colour the agent label column via the helper.
- [x] 2.3 Render test: each agent's row label renders in the expected colour.

## 3. Armed-for-delete chip

- [x] 3.1 When `c.ID == m.pendingDelete`, render the row's trailing edge as `[delete? x to confirm · esc]` in `s.Semantic.Danger`. Keep the agent label + timestamp + truncated preview visible.
- [x] 3.2 Update or add a test covering the armed-row layout.

## 4. Detail pane trim + sub-section grouping

- [x] 4.1 Trim `renderDetail` to ID + project + relative `last active` (drop the absolute timestamp).
- [x] 4.2 Group the remaining fields into `Identity` and `Activity` sub-sections with the design-system 2-cell indent step.
- [x] 4.3 Wrap long project paths and first-prompt text inside the detail pane so wide-mode columns do not bleed into each other.

## 5. `p` transcript preview modal

- [x] 5.1 Add `conversationPreviewOverlay` model rendering the recent ~30 messages via Glamour (using the design-system theme from `redesign-tui-notes`; if that change hasn't landed, ship a local theme that matches).
- [x] 5.2 Wire `p` key in `app.go` with `!modalCapturingText()` guard.
- [x] 5.3 Unit test covering modal open / close.

## 6. Golden refresh

- [x] 6.1 Regenerate `internal/tui/testdata/golden/conversations.txt` at 119×40 (existing narrow golden).
- [x] 6.2 Add `internal/tui/testdata/golden/conversations_modal.txt` capturing the `p` overlay at 120×40.

## 7. `bubbles/spinner` for transcript scan

- [x] 7.1 Replace the muted `Loading conversations from …` placeholder with `bubbles/spinner`.
- [x] 7.2 Render test: spinner present during initial scan; replaced by content after data arrives.

## 8. Logical transcript merge

- [x] 8.1 Add in-memory merge support for Claude parent + nested subagent fragments and duplicate Codex rollout UUIDs.
- [x] 8.2 Keep agent transcript files untouched; preserve `Conversation.Path` as the primary path and expose all fragments through `Conversation.Paths`.
- [x] 8.3 Make recent-message preview, message count, recency, and delete operate across all merged fragments.
- [x] 8.4 Add focused data-layer tests for Claude subagent merge, Codex duplicate UUID merge, and multi-fragment delete.
- [x] 8.5 Strip leading skill-invocation prefixes from first-prompt previews and transcript-preview user turns.
- [x] 8.6 Use compact, unbracketed row agent labels.
- [x] 8.7 Add Cursor JSONL and Antigravity JSON chat discovery so non-Claude rows expose previews, recent messages, and message counts where the transcript format allows it.
- [x] 8.8 Strip leading Codex AGENTS.md instruction bundles from previews and prompt counts.
- [x] 8.9 Render wide Conversations columns as framed panes with a wider gutter.

## 9. Validate

- [x] 9.1 Run `go test ./...` and `make lint`; confirm green.
- [x] 9.2 Run `openspec validate redesign-tui-conversations --type change --strict --no-interactive`.
- [x] 9.3 Run `openspec instructions apply --change redesign-tui-conversations --json` and confirm `state != "blocked"`.
- [x] 9.4 After implementation: `openspec archive redesign-tui-conversations --yes`.
