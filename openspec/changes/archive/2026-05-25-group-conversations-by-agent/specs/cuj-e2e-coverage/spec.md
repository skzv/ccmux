## MODIFIED Requirements

### Requirement: Conversations CUJ is verified end to end

The system SHALL list past agent conversations through known-agent
navigation, resume a conversation into a new session, and delete a
conversation transcript. Each SHALL be covered by an e2e test using fixture
transcripts in the isolated `$HOME`.

#### Scenario: List conversations

- **WHEN** the isolated `$HOME` contains agent transcript fixtures
- **THEN** the Conversations screen shows Claude, Codex, Cursor, and Agy in
  the agent navigation
- **AND** the focused agent view lists that agent's conversations sorted by
  recency with their project and agent

#### Scenario: Empty agent section

- **WHEN** the isolated `$HOME` contains no visible conversations for a
  focused known supported agent
- **THEN** the Conversations screen shows that agent's view with a clear
  empty state

#### Scenario: Switch agent sections

- **WHEN** the Conversations screen contains multiple agent sections
- **THEN** Tab/Shift+Tab or left/right key input changes the focused agent
  section without moving row selection across section boundaries

#### Scenario: Resume a conversation

- **WHEN** the user resumes a listed conversation
- **THEN** a new tmux session is created for that conversation's project
  and the resume command targets the correct conversation id

#### Scenario: Delete a conversation

- **WHEN** the user confirms deletion of a conversation
- **THEN** the transcript file is removed and the row disappears from the
  next listing
