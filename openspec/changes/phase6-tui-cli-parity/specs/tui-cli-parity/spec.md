# Spec: TUI ⇄ CLI Feature Surface Parity

## Requirements

### Requirement: Project Agent Switching via CLI
The CLI MUST provide `ccmux project set-agent <project> <agent>` to update a project's `.ccmux/agent` sidecar while keeping `ccmux project <name>` working.

#### Scenario: Set project agent to codex
- **WHEN** the user runs `ccmux project set-agent demo codex`
- **THEN** `<projects-root>/demo/.ccmux/agent` is atomically written with `codex` and the command exits 0.

#### Scenario: Reject unknown agent or missing project
- **WHEN** the user passes an unknown agent ID or non-existent project name
- **THEN** the command returns a descriptive non-zero error without modifying disk.

### Requirement: Note Creation via CLI
The CLI MUST provide `ccmux notes new <project> <filename> [--title <title>] [--edit]` backed by `notes.Vault.Create`.

#### Scenario: Create note with title and no overwrite
- **WHEN** the user runs `ccmux notes new demo docs/plan --title "My Plan"`
- **THEN** `<projects-root>/demo/docs/plan.md` is created containing `# My Plan\n\n` and a second identical invocation fails with an already-exists error.

### Requirement: Remote Project Creation via CLI
`ccmux new` MUST accept `--host <name>` to create a project and launch its session on a configured remote host via `POST /v1/projects`.

#### Scenario: Create project on remote host without TTY
- **WHEN** the user runs `ccmux new --host mini api --agent codex` without a TTY
- **THEN** `ccmux` sends `POST /v1/projects` with `{"name":"api","agent":"codex"}` and prints the remote attach hint.

### Requirement: Agent Config Toggles via CLI
The CLI MUST provide `ccmux agents config [agent]` (read) and `ccmux agents config set <agent> <key> <value>` (write) for `claude`, `codex`, and `antigravity`.

#### Scenario: Toggle YOLO and effort via CLI
- **WHEN** the user runs `ccmux agents config set claude effort high` or `ccmux agents config set codex yolo true`
- **THEN** the corresponding agent settings file is updated via the existing config package with backup and preservation of unknown keys.

### Requirement: General Config Inspection and Editing via CLI
The CLI MUST provide `ccmux config get [key]`, `ccmux config list [--json]`, and `ccmux config set <key> <value>` using comment-preserving saves and Settings-equivalent validation.

#### Scenario: Round-trip config get/set
- **WHEN** the user runs `ccmux config set sessions.attach_mode exclusive` followed by `ccmux config get sessions.attach_mode`
- **THEN** `exclusive` is printed and invalid values are rejected with a non-zero exit code.

### Requirement: Session Pane Preview and Conversation Read via CLI
The CLI MUST provide `ccmux preview <session> [--lines N] [--host <name>] [--json]` and `ccmux read-conversation <id> [--agent <agent>] [--limit N] [--json]`.

#### Scenario: Preview session and read conversation
- **WHEN** the user runs `ccmux preview c-demo --lines 20` or `ccmux read-conversation <id> --json`
- **THEN** sanitized pane lines or recent conversation messages are printed.

### Requirement: Host Management and Mobile Pairing Modals in TUI
The Network screen MUST support `a` (add host modal), `x` (remove configured host with confirmation), and `P` (mobile pairing QR modal).

#### Scenario: Add and remove configured host from Network screen
- **WHEN** the user submits the Add Host modal with a valid name and address
- **THEN** the host is persisted to `config.toml` and appears on refresh; pressing `x` on a configured host prompts for confirmation and removes it.

#### Scenario: Open Mobile Pairing QR modal
- **WHEN** the user presses `P` on the Network screen
- **THEN** `ccmux` fetches `/v1/pair-token` asynchronously and displays the QR code and URL in a modal (or toasts an error if the daemon/tailnet listener is unavailable).

### Requirement: Daemon Lifecycle and Claude MCP Toggle in TUI
The TUI MUST expose `D` on Network/Settings to start or restart `ccmuxd`, and `w` on Agents → Claude to toggle `ccmux-mcp` registration.

#### Scenario: Toggle Claude MCP registration in TUI
- **WHEN** the user presses `w` on the Agents → Claude sub-tab
- **THEN** `ccmux` registers or unregisters `ccmux-mcp` asynchronously, reloads the Claude MCP section, and displays a confirmation toast.
