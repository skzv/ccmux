## ADDED Requirements

### Requirement: Agent Executable Candidates

ccmux SHALL be able to enumerate all executable candidates for any
supported agent binary name on a PATH-like string, preserving PATH
resolution order and deduplicating repeated paths.

#### Scenario: Multiple agent installations are on PATH

- **GIVEN** a PATH containing two directories that each contain an
  executable for a supported agent
- **WHEN** ccmux enumerates that agent's candidates
- **THEN** both executable paths are returned in PATH order

#### Scenario: Duplicate PATH entries are present

- **GIVEN** the same directory appears more than once in PATH
- **WHEN** ccmux enumerates candidates
- **THEN** the executable path is returned only once

### Requirement: Configured Agent Command

ccmux SHALL persist user-selected commands for supported agents and use
the relevant command for session launches when the setting is present.

#### Scenario: Agent command is configured

- **GIVEN** `agents.<agent>.command` is set to an absolute executable
  path
- **WHEN** ccmux builds a fresh launch command for that agent
- **THEN** the command starts that configured executable, not the literal
  default binary token

#### Scenario: Agent command is not configured

- **GIVEN** `agents.<agent>.command` is empty
- **WHEN** ccmux builds a launch command for that agent
- **THEN** ccmux preserves the existing binary-on-PATH behavior

### Requirement: Setup-Time Agent Selection

ccmux SHALL ask the user which executable to use during the setup AI
agents dependency section and persist that selection when setup detects
multiple executables on PATH for a supported agent and no command is
already configured for that agent.

#### Scenario: Multiple candidates found during setup

- **GIVEN** setup runs with two executable candidates for a supported
  agent on PATH
- **AND** no command is configured for that agent
- **WHEN** setup reaches the AI agents dependency section
- **AND** the user selects one candidate
- **THEN** ccmux writes that path to `agents.<agent>.command`
- **AND** the wizard continues to optional dependency installation and
  later setup steps

#### Scenario: Existing selection is present

- **GIVEN** `agents.<agent>.command` is already set
- **WHEN** setup runs
- **THEN** ccmux preserves the existing configured command unless the
  user edits the config directly

### Requirement: Daemon Service PATH Includes Configured Commands

ccmux SHALL include the parent directory of configured agent commands in
the managed daemon service PATH before generic package-manager paths.

#### Scenario: Configured Claude command lives under NVM

- **GIVEN** `agents.claude.command` is
  `/Users/me/.nvm/versions/node/v23.9.0/bin/claude`
- **WHEN** ccmux installs the daemon service
- **THEN** the generated service PATH contains
  `/Users/me/.nvm/versions/node/v23.9.0/bin` before Homebrew paths

### Requirement: Doctor Reports Multi-Install State

`ccmux doctor` SHALL report enough executable information for supported
agents for a user to understand which installation ccmux will use.

#### Scenario: Multiple agent commands are visible

- **GIVEN** multiple candidates for a supported agent are visible on PATH
- **WHEN** the user runs `ccmux doctor`
- **THEN** the output lists the configured agent command when present,
  the PATH-first candidate, and all visible candidates
