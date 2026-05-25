## ADDED Requirements

### Requirement: New Session Attach Preserves Existing Clients

ccmux SHALL attach to sessions it has just created without passing
tmux's detach-other-clients flag, regardless of the user's configured
`sessions.attach_mode`.

#### Scenario: Local Sessions tab creates a session

- **WHEN** the user creates a local session from the Sessions tab new-session form
- **THEN** ccmux creates the tmux session
- **AND** the subsequent attach command omits `-d`
- **AND** other tmux clients attached to the same tmux server remain attached

#### Scenario: Remote Sessions tab creates a session

- **WHEN** the user creates a remote session from the Sessions tab new-session form
- **THEN** the remote daemon creates the tmux session
- **AND** the subsequent ssh or mosh attach command omits `-d`
- **AND** other tmux clients on the remote tmux server remain attached

#### Scenario: CLI shell creates a session

- **WHEN** the user runs `ccmux shell` for a local or remote host
- **THEN** ccmux creates the requested bare session
- **AND** the subsequent local or remote attach command omits `-d`

#### Scenario: Nested tmux creates a session

- **WHEN** ccmux is already running inside tmux and creates a new local session
- **THEN** ccmux switches the current client to the new session
- **AND** it does not attempt a nested `tmux attach-session -d`

### Requirement: Existing Session Attach Remains Configurable

ccmux SHALL continue to honor `sessions.attach_mode` when the user
explicitly attaches to an existing session.

#### Scenario: Existing local session attach uses exclusive mode

- **WHEN** `sessions.attach_mode` is set to exclusive
- **AND** the user attaches to an already-running local session
- **THEN** ccmux constructs a tmux attach command with `-d`

#### Scenario: Existing remote session attach uses mirror mode

- **WHEN** `sessions.attach_mode` is set to mirror
- **AND** the user attaches to an already-running remote session
- **THEN** ccmux constructs a remote tmux attach command without `-d`
