## ADDED Requirements

### Requirement: Per-agent command catalog sourced from the owning host

The bridge SHALL be able to obtain, for a given session, the catalog of commands the session's agent supports, computed on the host that runs the session. For a Claude session, the catalog MUST merge Claude's built-in slash-commands with the host user's own commands and skills (`internal/claudeconfig` `ListCommands`/`ListSkills`). For other agents, the catalog is that agent's curated command set. The catalog is exposed by the daemon so a peer session returns its *own* host's catalog, not the bot host's.

#### Scenario: Claude catalog includes user-defined commands
- **WHEN** a Claude session's catalog is requested on a host with a custom command at `~/.claude/commands/deploy.md`
- **THEN** the catalog includes Claude's built-in slash-commands and `/deploy` with its description

#### Scenario: Catalog matches the session's agent
- **WHEN** the session runs Codex (not Claude)
- **THEN** the catalog is Codex's command set and does not offer Claude-only slash-commands

#### Scenario: Peer session reflects the peer's commands
- **WHEN** the targeted session runs on peer `mini`
- **THEN** the catalog is computed on `mini` and reflects `mini`'s user-defined commands, not the bot host's

### Requirement: Autocomplete and previews of agent commands

The bridge SHALL surface a session's agent command catalog as Telegram inline-query autocomplete, and every offered command MUST carry its description so the user previews what it does before sending. This is the primary "available CLI commands" discovery surface.

#### Scenario: Inline autocomplete matches an agent command
- **WHEN** an allowlisted user types an inline query targeting a Claude session with `mod`
- **THEN** the results include `/model` with its description

#### Scenario: Picker entries show descriptions
- **WHEN** the user opens the command picker for a session
- **THEN** each entry shows the command and a one-line description

### Requirement: Send an agent command from the catalog

Selecting a catalog command SHALL deliver it to the session's agent via send-keys on the owning host (the command text followed by Enter). A command that takes an argument MUST prompt for it (free text) or offer inline-keyboard choices before sending, rather than sending an incomplete command.

#### Scenario: Send an argument-free command
- **WHEN** the user picks `/compact` for `local:build`
- **THEN** the bridge sends `/compact` then Enter to that pane and confirms it was sent

#### Scenario: Argument-taking command prompts first
- **WHEN** the user picks `/model` (which takes an argument)
- **THEN** the bridge asks for or offers the value and only then sends `/model <value>`

### Requirement: Send a free-form prompt to the agent

The bridge SHALL let an allowlisted user send a free-form prompt to a session's agent — arbitrary text routed to the pane followed by Enter — so Telegram is a full input channel for the agent, not only a command picker.

#### Scenario: Prompt delivered to the agent
- **WHEN** an allowlisted user sends a prompt targeting `mini:api`
- **THEN** the text is delivered to that session's agent followed by Enter

### Requirement: Agent control is structured control, not arbitrary exec

Sending a catalog command or a free-form prompt SHALL be part of the allowlist-gated control tier and MUST be available without `allow_exec`, because the surface is the agent's own curated commands plus prompts (the agent's normal input), not raw shell. Raw key sequences / shell remain gated by `allow_exec` per `telegram-commands`.

#### Scenario: Control available without the exec gate
- **WHEN** `allow_exec = false` and an allowlisted user sends a catalog command or a prompt
- **THEN** the bridge delivers it, while a raw `/run` shell command for the same session is still refused

### Requirement: CLI discovery of the catalog

`ccmux agent commands [--agent <id>] [--session <name>]` SHALL print the resolved command catalog, so the same data is reachable from the CLI per the feature-surface policy and is scriptable.

#### Scenario: Print the Claude catalog
- **WHEN** the user runs `ccmux agent commands --agent claude`
- **THEN** it prints Claude's built-in slash-commands plus the host's user-defined commands and skills
