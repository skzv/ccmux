## 1. Shared per-agent palette helper

- [x] 1.1 Add `styles.AgentAccent(id agent.ID) lipgloss.Style` (or method on `Styles`). Mapping: Claude=Mauve, Codex=Sky, Antigravity=Peach, Cursor=Teal. Unknown IDs default to Mauve.
- [x] 1.2 Migrate `dashboard.go`'s `agentSectionHeading` to consume the helper.
- [x] 1.3 Unit test covering all four IDs + an unknown ID default.

## 2. Agents sub-tab row uses the helper

- [x] 2.1 Update `agentsModel.renderSubtabs` so each sub-tab label renders in `styles.AgentAccent(id)` when active; muted when inactive. Keep the `◆ ` glyph as the active-marker prefix.
- [x] 2.2 Add a render test asserting the colour selection per sub-tab.

## 3. `internal/cursorusage` package

- [x] 3.1 Add `internal/cursorusage/cursorusage.go` with a `Summary` struct and an `Open(dbPath string) (Summary, error)` function. Use `modernc.org/sqlite` (vendor it; ensure `go vet ./...` is clean on the new dep).
- [x] 3.2 Implement the SQLite queries: distinct conversationId count, top-5 models by request count, AI lines last 7d (`tabLinesAdded + composerLinesAdded`), latest activity timestamp.
- [x] 3.3 Return `ErrNotInstalled` (sentinel) when the DB path doesn't exist; consumers render an empty-state placeholder.
- [x] 3.4 Add `internal/cursorusage/cursorusage_test.go` against a fixture SQLite file under `testdata/`. Build the fixture programmatically in `TestMain` so it doesn't need to ship as a binary blob.

## 4. Cursor sub-tab population

- [x] 4.1 Replace the inline `Cursor settings are managed by Cursor CLI.` string in `agents.go` with a render that reads `cursorusage.Open(~/.cursor/ai-tracking/ai-code-tracking.db)`.
- [x] 4.2 Render the summary in the same sub-section style as the Claude sub-tab (Conversation count, Models used, AI lines this week, Last activity).
- [x] 4.3 Render the empty-state placeholder when `ErrNotInstalled`.

## 5. Per-sub-tab HelpBar

- [x] 5.1 Update `agentsModel.HelpBarProps(width)` to switch on `m.active` and return the keys relevant to that sub-tab plus the common ones.
- [x] 5.2 Tests covering each sub-tab's HelpBarProps return value.

## 6. Claude sub-screen sub-sections

- [x] 6.1 Group `claudeModel.View()` content into `Defaults` (model, effort, alwaysThinking, yolo) and `Config files` (CLAUDE.md, settings.json) with the 2-cell indent step.

## 7. Fix stale `TranscriptsRoot`

- [x] 7.1 Update `internal/agent/cursor.go:27` to return `~/.cursor/chats/` (real layout).

## 8. Goldens

- [x] 8.1 Add `internal/tui/testdata/golden/agents_claude.txt` (default sub-tab).
- [x] 8.2 Add `internal/tui/testdata/golden/agents_cursor.txt` (data populated from a fixture SQLite).
- [x] 8.3 Add `internal/tui/testdata/golden/agents_cursor_empty.txt` (no `~/.cursor` present).

## 9. `bubbles/spinner` for SQLite read

- [x] 9.1 Cache the `cursorusage.Summary` for 30s; spinner shown during initial load.

## 10. Validate

- [x] 10.1 Run `go test ./...` and `make lint`; confirm green.
- [x] 10.2 Run `openspec validate redesign-tui-agents --type change --strict --no-interactive`.
- [x] 10.3 Run `openspec instructions apply --change redesign-tui-agents --json` and confirm `state != "blocked"`.
- [ ] 10.4 After merge: `openspec archive redesign-tui-agents --yes`.
