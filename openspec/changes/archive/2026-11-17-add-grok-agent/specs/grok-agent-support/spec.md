## ADDED Requirements

### Requirement: Grok Agent Registration

ccmux SHALL register Grok Build CLI as a supported interactive agent
with canonical ID `grok`, display name `Grok`, and default binary
`grok`.

#### Scenario: Grok is parsed from user input

- **WHEN** ccmux parses the agent ID `grok` (any case, surrounding
  whitespace trimmed)
- **THEN** parsing succeeds with the Grok agent ID

#### Scenario: Unknown agent IDs are rejected

- **WHEN** ccmux parses an agent ID that is not a supported agent
- **THEN** parsing fails so callers fall back to the default agent

#### Scenario: Supported agents are enumerated

- **WHEN** ccmux enumerates supported agents
- **THEN** Grok appears after the existing Claude, Codex, Antigravity,
  Cursor, and pi agents
- **AND** Claude remains the default agent

### Requirement: Grok Launch Commands

ccmux SHALL launch Grok Build CLI with the command dialect documented
for interactive Grok sessions.

#### Scenario: New Grok project starts an interactive session

- **GIVEN** a project is configured with agent `grok`
- **WHEN** ccmux starts a new project session
- **THEN** the tmux pane command starts `grok`

#### Scenario: Existing Grok project resumes the latest session

- **GIVEN** a project is configured with agent `grok`
- **WHEN** ccmux attaches and must create a missing tmux session for an
  existing project
- **THEN** the tmux pane command starts `grok --continue`
- **AND** falls back to `grok`, then an interactive shell, if resume
  fails

### Requirement: Grok Resume Args

ccmux SHALL define the Grok-specific argument vector for resuming a
specific Grok conversation.

#### Scenario: Resume a specific Grok conversation

- **GIVEN** a Grok conversation ID `abc-123`
- **WHEN** ccmux builds explicit resume args for that conversation
- **THEN** the args are `grok --resume abc-123`

#### Scenario: Empty conversation ID yields no resume args

- **WHEN** ccmux builds explicit resume args with an empty conversation
  ID
- **THEN** no resume args are produced

### Requirement: Grok Configured Command Resolution

ccmux SHALL allow users to configure the Grok executable path and use
that path for Grok session launches and resume commands.

#### Scenario: Grok command is configured

- **GIVEN** `agents.grok.command` is set to an executable path
- **WHEN** ccmux builds a Grok launch command
- **THEN** the command uses the configured executable path instead of
  the literal `grok` token

#### Scenario: Grok command is not configured

- **GIVEN** `agents.grok.command` is empty
- **WHEN** ccmux builds a Grok launch command
- **THEN** ccmux preserves the binary-on-PATH behavior using `grok`

#### Scenario: Grok command participates in setup selection

- **GIVEN** multiple Grok executables are visible on PATH
- **AND** no Grok command is configured
- **WHEN** the user runs setup
- **THEN** ccmux offers the visible Grok candidates for selection and
  persists the selected path to `agents.grok.command`

#### Scenario: Configured Grok command directory is on the daemon PATH

- **GIVEN** `agents.grok.command` is an executable under a non-standard
  directory
- **WHEN** ccmux installs the managed daemon service
- **THEN** the generated service PATH contains that directory before
  generic package-manager paths

### Requirement: Grok Initial Prompt Uses Shared Context File

ccmux SHALL bootstrap a new Grok project with the cross-agent
`AGENTS.md` convention used by the other non-Claude agents, so project
context persists in a single shared file.

#### Scenario: New Grok project is bootstrapped

- **GIVEN** ccmux creates a new project configured with agent `grok`
- **WHEN** ccmux composes the initial prompt typed into the agent
- **THEN** the prompt instructs the agent to write `AGENTS.md` at the
  project root for persistent context

### Requirement: Grok User-Facing Surfaces

ccmux SHALL include Grok in user-facing surfaces that enumerate
supported agents.

#### Scenario: Agent help text lists Grok

- **WHEN** a user views CLI `--agent` help or receives an unknown-agent
  error
- **THEN** Grok is listed alongside Claude, Codex, Antigravity, Cursor,
  and pi

#### Scenario: Doctor reports Grok install state

- **WHEN** a user runs `ccmux doctor`
- **THEN** the AI agent diagnostics include Grok install and configured
  command state

#### Scenario: TUI agent picker offers Grok when available

- **GIVEN** Grok is installed on PATH or a Grok command is configured
- **WHEN** the user opens the new-project agent picker in the TUI
- **THEN** Grok is offered as a selectable agent
