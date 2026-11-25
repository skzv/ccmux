# Proposal: Phase 6 — TUI ⇄ CLI Feature Surface Parity

## Why

`AGENTS.md` establishes the Feature Surface Policy: every user-visible feature must be reachable from both the TUI (daily driver) and the CLI (muscle memory and scripting). Our codebase audit identified nine remaining parity gaps where capabilities existed on only one surface, creating discoverability cliffs and breaking scriptability / keyboard-driven workflows.

## What Changes

### TUI → CLI Parity (new CLI subcommands & flags)
1. **`ccmux project set-agent <project> <agent>`** — switches a project's `.ccmux/agent` sidecar (matching the Projects tab `a` binding), while preserving `ccmux project <name>`.
2. **`ccmux notes new <project> <filename> [--title <title>] [--edit]`** — creates a new markdown note safely with `O_EXCL` and vault confinement (matching the Notes tab `n` modal), via shared `notes.Vault.Create`.
3. **`ccmux new --host <name> <project> [--agent <id>]`** — creates a project and starts its session on a configured remote host via `POST /v1/projects` (matching the Projects tab `n` modal).
4. **`ccmux agents config [agent]` & `ccmux agents config set <agent> <key> <value>`** — reads and toggles agent-level settings (`model`, `effort`, `always-thinking`, `yolo`) for Claude, Codex, and Antigravity (matching the Agents tab).
5. **`ccmux config get [key]` & `ccmux config set <key> <value>`** — reads and edits general `config.toml` preferences with comment preservation and Settings-equivalent validation.
6. **`ccmux preview <session> [--lines N] [--host <name>] [--json]`** — prints the trailing lines of a session's agent pane via `GET /v1/sessions/<name>/preview` (or local `tmux.CapturePane` fallback).
7. **`ccmux read-conversation <id> [--agent <agent>] [--limit N] [--json]`** — renders recent messages from a past conversation transcript (matching the Conversations tab `p` preview overlay).

### CLI → TUI Parity (new TUI modals & actions)
8. **Network screen host & pairing management:**
   - `a` opens an Add Host modal (`name`, `address`, `user`, `ssh port`, `mosh`) writing to `config.toml` via `config.Update`.
   - `x` prompts to remove a configured host via the confirmation modal.
   - `P` opens a Mobile Pairing modal displaying the half-block QR code and pairing URL from `/v1/pair-token`.
9. **Daemon lifecycle & Claude MCP toggle in TUI:**
   - `D` on Network and Settings starts `ccmuxd` when offline or restarts it via `daemonservice.Restart()` when running.
   - `w` on Agents → Claude registers or unregisters `ccmux-mcp` via `setupwizard.RegisterMCP` / `UnregisterMCP` (toggling read-only registration without leaving the TUI).

## Non-Goals

- Adding second-wave `[agents.<id>].command` config fields (tracked separately).
- Remote notes creation over HTTP (notes remain read-only on remote peers).
- Rewriting existing golden layouts unnecessarily; new TUI affordances use modals and help-bar hints so steady-state layouts stay clean.
