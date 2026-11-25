# Tasks: Phase 6 — TUI ⇄ CLI Feature Surface Parity

## 1. Shared Backend & Notes Creation
- [x] Add `notes.Vault.Create(filename, title string)` in `internal/notes/notes.go` with `O_EXCL` and confinement tests.
- [x] Refactor `internal/tui/notes_editor.go` to delegate note creation to `notes.Vault.Create`.

## 2. TUI → CLI Parity Subcommands
- [x] Add `ccmux project set-agent <project> <agent>` in `cmd/ccmux/cmd/project.go`.
- [x] Add `ccmux notes new <project> <filename> [--title ...] [--edit]` in `cmd/ccmux/cmd/notes.go`.
- [x] Add `--host` support to `ccmux new` in `cmd/ccmux/cmd/subcommands.go` and `cmd/ccmux/cmd/remotehost.go`.
- [x] Add `ccmux agents config [agent]` & `ccmux agents config set <agent> <key> <value>` in `cmd/ccmux/cmd/agents.go`.
- [x] Add `ccmux config get/set/list` in `cmd/ccmux/cmd/configcmd.go` and wire into `root.go`.
- [x] Add `ccmux preview <session> [--lines N] [--host <name>] [--json]` in `cmd/ccmux/cmd/preview.go` and wire into `root.go`.
- [x] Add `ccmux read-conversation <id> [--agent <agent>] [--limit N] [--json]` in `cmd/ccmux/cmd/readconversation.go` and wire into `root.go`.
- [x] Add comprehensive CLI tests for all 7 new commands/flags.

## 3. CLI → TUI Parity Modals & Actions
- [x] Add Add Host modal (`internal/tui/addhost.go`) and wire `a` + `x` (remove configured host confirmation) on `ScreenNetwork`.
- [x] Add Mobile Pairing QR modal (`internal/tui/pairmodal.go`) and wire `P` on `ScreenNetwork` / `ScreenSettings`.
- [x] Add async Daemon start/restart (`D`) on `ScreenNetwork` / `ScreenSettings`.
- [x] Add async Claude MCP register/unregister toggle (`w`) on Agents → Claude (`internal/tui/claudeconfig.go`).
- [x] Update HelpBarProps, overlays, `modalCapturingText`, and all 8 `internal/i18n/locales/*.toml` catalogs.
- [x] Add TUI unit/teatest regression tests and ensure `styles_lint_test.go` + `i18n/sync_test.go` pass.

## 4. Docs, Verification & PR
- [x] Update `README.md` / CLI command list for new user-visible commands.
- [x] Run `go test -race -count=1 ./...`, `make lint`, `make test-e2e`, `make fuzz-quick`, `make tapes-check`, cross-compile, and `ccmux-crawl`.
- [x] Open PR, wait for green CI, and merge.
