# Design: Phase 6 — TUI ⇄ CLI Feature Surface Parity

## Architecture & Shared Primitives

### 1. Shared Note Creation (`internal/notes`)
Add `(v Vault) Create(filename, title string) (rel, abs string, err error)` in `internal/notes/notes.go`:
- Normalizes `filename`: trims space, appends `.md` if missing (case-insensitive), validates with `notes.CleanRel`.
- Confines parent directory creation and `O_WRONLY|O_CREATE|O_EXCL` file creation under `v.Root` (rejecting path traversal and symlinks).
- Writes `# <title>\n\n` when `title != ""`, else empty file.
- Both `internal/tui/notes_editor.go` (`createAndOpenNote`) and `cmd/ccmux/cmd/notes.go` (`notes new`) delegate to `Vault.Create`.

### 2. CLI Surface (`cmd/ccmux/cmd/`)
- **`project.go`**: Attach `newProjectSetAgentCmd()` (`set-agent <project> <agent>`) as a subcommand of `newProjectCmd()`, while keeping `ccmux project <name>` on the parent `RunE` (`Args: cobra.ExactArgs(1)`).
- **`notes.go`**: Add `new <project> <filename> [--title ...] [--edit]` under `newNotesCmd()`.
- **`subcommands.go` (`newNewCmd`)**: Add `--host` flag; when `!isLocalHost(host)`, call `rh.cli.NewProject(ctx, daemon.NewProjectRequest{Name, Agent})` and hand off to `remoteAttachCmd` (or print non-TTY hint).
- **`agents.go`**: Add `config` subcommand (`show` default / `set <agent> <key> <value>`) supporting `claude` (`model`, `effort`, `always-thinking`, `yolo`), `codex` (`effort`, `yolo`), and `antigravity` (`effort`, `yolo`), reusing `claudeconfig`, `codexconfig`, `antigravityconfig`.
- **`configcmd.go`**: Add `newConfigCmd()` (`config get [key]`, `config set <key> <value>`, `config list [--json]`) with a single key registry table mirroring Settings screen fields + daemon/session/conversation settings.
- **`preview.go`**: Add `newPreviewCmd()` (`preview <session> [--lines N] [--host <name>] [--json]`) backed by `daemon.Client.Preview` with local `tmux.CapturePane` fallback.
- **`readconversation.go`**: Add `newReadConversationCmd()` (`read-conversation <id> [--agent <agent>] [--limit N] [--json]`) resolving `<id>` via `pickByID` and reading messages via `conversations.RecentMessages`.

### 3. TUI Surface (`internal/tui/`)
- **Add Host Modal (`addhost.go`)**: 5-row form (`name`, `address`, `user`, `ssh port`, `mosh`) matching `newNoteFormModel` / `newSessionFormModel` styling (design tokens only — passes `styles_lint_test.go`). Triggered by `a` on `ScreenNetwork`, persists via `config.Update`, triggers host refresh + toast.
- **Remove Host (`network.go` + `app_update.go`)**: Triggered by `x` on a `hostSourceConfigured` row on `ScreenNetwork`; opens the existing `confirmationModel` (`confirmRemoveHost`), removes from `config.Update` on confirm, refreshes hosts + toast.
- **Mobile Pairing Modal (`pairmodal.go`)**: Triggered by `P` on `ScreenNetwork` (and `ScreenSettings`); calls `daemon.LocalClient().CreatePairToken(ctx)` asynchronously, renders half-block QR via `qrterminal.GenerateHalfBlock` into a buffer plus the URL inside a centered modal; closes on `esc`/`P`.
- **Daemon Lifecycle Action (`app_update.go`)**: Triggered by `D` on `ScreenNetwork` or `ScreenSettings`; probes `daemonservice.Probe()`, starts `ccmuxd` if not running or calls `daemonservice.Restart()` if running, runs asynchronously in a `tea.Cmd`, toasts outcome, and triggers refresh.
- **Claude MCP Toggle (`claudeconfig.go`)**: Triggered by `w` on Agents → Claude; checks `setupwizard.MCPRegistrationStatus()` asynchronously and calls `setupwizard.RegisterMCP` (read-only) when unregistered or `setupwizard.UnregisterMCP` when ours is registered, reloading the Claude MCP list and toasting the result.
- **i18n**: Every new `tr(...)` string is added to all 8 `internal/i18n/locales/*.toml` files and verified by `TestTUILKeysSynced`.
