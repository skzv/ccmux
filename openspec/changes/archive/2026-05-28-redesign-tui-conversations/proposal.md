## Why

PR #114 (`redesign-tui-charm`) landed the design-system foundation and applied mockup-6 fully to the home/Dashboard screen. The Conversations tab received the structural plumbing — `components.RenderListRow` selection migration, dropped inline footer hint, per-screen `HelpBarProps` (with the dynamic `H headless: hidden/shown` label) — but not the visual polish: the agent-section nav row, the conversation rows themselves, and the right-hand detail pane still read flat. The dashboard's Usage panel introduced per-agent colour coding (Claude=mauve, Codex=sky, Antigravity=peach); the Conversations tab is the next natural surface to inherit it.

This change applies the per-agent colour vocabulary to Conversations, trims the detail pane, merges scattered transcript fragments into one logical conversation row, and adds a transcript preview modal so the user can read more of a conversation without leaving the tab.

## What Changes

- **Per-agent colour coding on the section nav**: `renderAgentNav` currently emphasises the active section with `▸ ` + emphasis style and renders inactive sections in muted. Apply the dashboard's agent palette so `Claude N` renders mauve when active, `Codex M` renders sky, `Antigravity K` renders peach. Inactive stays muted. The colour carries through to the row's agent label column (`renderConversationRowContent`).
- **HelpBar audit**: confirm every advertised key has a working handler (`enter resume`, `x delete`, `tab sections`, `H headless`, `r refresh`).
- **Detail pane trim** (`renderDetail`, `internal/tui/conversations.go:557`): keep ID, project, last-active (move the absolute timestamp behind the new preview modal to fix the existing golden flake). Drop the multi-line headless-mode explanation when not in headless mode. Keep the resume keybind hint.
- **Status chips for armed-for-delete state**: today `delete this conversation? press x to confirm · esc cancels` renders as an inline status-error string that displaces the row's preview. Promote to a bracketed `[delete? x to confirm · esc]` chip that lives at the row's trailing edge so the agent label and timestamp stay visible.
- **Compact bare agent labels**: row labels use `claude`, `codex`, `cursor`, and `agy` without bracket adornment, and the row layout keeps only compact gaps between agent, timestamp, and prompt preview.
- **`p` keybind + transcript preview modal**: open a focused overlay showing the conversation's recent message thread (Glamour-rendered, last ~30 messages). Parallel to the Dashboard's `u` usage overlay. Closes the timestamp-drift problem in the detail pane by moving rich content to the modal where the absolute timestamp isn't load-bearing.
- **Logical transcript merge**: keep agent-owned transcript files untouched, but merge known fragments in memory before rendering. Claude parent transcripts and nested `<uuid>/subagents/*.jsonl` files collapse to one parent conversation row; duplicate Codex rollout UUIDs collapse to one Codex row. Message count, preview modal, recency, and delete operate across all fragments.
- **Non-Claude metadata parity**: Codex rows read cwd from either top-level or payload metadata and first prompts from both `user_message` and `response_item` events. Cursor JSONL transcripts under `~/.cursor/projects/**/agent-transcripts/` are listed. Gemini-style Antigravity JSON chats under `~/.gemini/tmp/**/chats/` are listed with previews/counts while opaque `.pb` files remain ID/mtime-only.
- **Sub-section heading hierarchy in the detail pane**: when wide, group the existing facts into `Identity` (ID, project) and `Activity` (last active, preview) with the design-system 2-cell indent hierarchy.
- **Per-screen golden refresh**: `internal/tui/testdata/golden/conversations.txt` already exists from PR #114 at 119×40. Regenerate at the new layout. Once the preview-modal pattern is in, capture a third golden at 120×40 with the modal open.
- **`bubbles/spinner` for transcript scan**: replace the muted `Loading conversations from ~/.claude, ~/.codex, ~/.gemini/antigravity-cli …` placeholder with a real spinner. Active during the initial walk and any refresh.

**Non-goals:**

- No physical rewriting, concatenating, moving, or normalizing of Claude/Codex-owned transcript files on disk.
- No changes to the resume flow (`enter`): a merged row still resumes by the agent's logical conversation/session ID.
- The OpenAI Codex cost estimator and Cursor SQLite usage aggregation are separate changes.

## Capabilities

### Modified Capabilities

- `tui-design-system`: adds Conversations-specific scenarios. New requirement for per-agent colour coding (Claude=mauve, Codex=sky, Antigravity=peach) carried across the screens that surface multiple agents (Dashboard's Usage panel and Conversations' section nav). New scenario for chip-based armed-state rendering.

## Impact

- **Affected code:** `internal/tui/conversations.go` (renderAgentNav colours, renderConversationRowContent chips, renderDetail trim + grouping), `internal/tui/app.go` (overlay routing for the `p` preview modal), `internal/conversations/` (logical fragment merge, recent-message reads, message counts, guarded multi-fragment delete).
- **Tests:** existing `conversations_test.go` assertions stay; new tests for agent colour mapping, armed-state chip, Claude subagent fragment merge, duplicate Codex rollout merge, and multi-fragment delete; the `conversations.txt` golden regenerates; a new `conversations_modal.txt` golden captures the preview overlay.
- **Dependencies:** no new third-party. Uses `bubbles/spinner` and `glamour` (already vendored).
- **User-visible:** Conversations section nav and row agent labels render in the agent's accent colour; the detail pane is shorter and grouped; pressing `p` opens a transcript preview; armed-for-delete state is a chip on the row's trailing edge.
- **CLI:** `list-conversations --json` may include `Paths` for merged rows; `delete-conversation` confirms every fragment path before deleting a merged row.
