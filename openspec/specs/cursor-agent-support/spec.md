# cursor-agent-support Specification

## Purpose
TBD - created by archiving change integrate-cursor. Update Purpose after archive.
## Requirements
### Requirement: Cursor Agent Registration

ccmux SHALL register Cursor CLI as a supported interactive agent with
canonical ID `cursor`, display name `Cursor`, and default binary
`cursor-agent`.

#### Scenario: Cursor is parsed from user input

- **WHEN** ccmux parses the agent ID `cursor`
- **THEN** parsing succeeds with the Cursor agent ID

#### Scenario: Supported agents are enumerated

- **WHEN** ccmux enumerates supported agents
- **THEN** Cursor appears after the existing Claude, Codex, and
  Antigravity agents

### Requirement: Cursor Launch Commands

ccmux SHALL launch Cursor CLI with the command dialect documented for
interactive Cursor Agent sessions.

#### Scenario: New Cursor project starts an interactive session

- **GIVEN** a project is configured with agent `cursor`
- **WHEN** ccmux starts a new project session
- **THEN** the tmux pane command starts `cursor-agent`

#### Scenario: Existing Cursor project resumes latest chat

- **GIVEN** a project is configured with agent `cursor`
- **WHEN** ccmux attaches and needs to create a missing tmux session for
  an existing project
- **THEN** the tmux pane command starts `cursor-agent resume`
- **AND** falls back to `cursor-agent`, then an interactive shell, if
  resume fails

### Requirement: Cursor Configured Command Resolution

ccmux SHALL allow users to configure the Cursor executable path and use
that path for Cursor session launches and resume commands.

#### Scenario: Cursor command is configured

- **GIVEN** `agents.cursor.command` is set to an executable path
- **WHEN** ccmux builds a Cursor launch command
- **THEN** the command uses the configured executable path instead of
  the literal `cursor-agent` token

#### Scenario: Cursor command participates in setup selection

- **GIVEN** multiple Cursor agent executables are visible on PATH
- **AND** no Cursor command is configured
- **WHEN** the user runs setup
- **THEN** ccmux offers the visible Cursor candidates for selection and
  persists the selected path to `agents.cursor.command`

### Requirement: Cursor Resume Args

ccmux SHALL define the Cursor-specific argument vector for resuming a
specific Cursor conversation.

#### Scenario: Resume a specific Cursor conversation

- **GIVEN** a Cursor conversation ID `abc-123`
- **WHEN** ccmux builds explicit resume args for that conversation
- **THEN** the args are `cursor-agent --resume abc-123`

### Requirement: Cursor User-Facing Surfaces

ccmux SHALL include Cursor in user-facing surfaces that enumerate
supported agents.

#### Scenario: Agent help text lists Cursor

- **WHEN** a user views CLI help or receives an unknown-agent error
- **THEN** Cursor is listed alongside Claude, Codex, and Antigravity

#### Scenario: Doctor reports Cursor install state

- **WHEN** a user runs `ccmux doctor`
- **THEN** the AI agent diagnostics include Cursor install and configured
  command state

