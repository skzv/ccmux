## ADDED Requirements

### Requirement: Conversations are navigated by known agent

The Conversations TUI SHALL render an agent navigation control ordered
Claude, Codex, Cursor, then Agy. The screen SHALL render only the focused
agent's visible conversations at one time. The focused agent view SHALL
contain only conversations for that agent and SHALL retain newest-first
ordering.

#### Scenario: Agent navigation renders in known order
- **GIVEN** the visible conversation set contains Claude, Codex, Cursor,
  and Antigravity conversations with interleaved timestamps
- **WHEN** the user opens the Conversations tab
- **THEN** the agent navigation lists Claude, Codex, Cursor, and Agy in
  that order
- **AND** the conversation list renders only the focused agent's
  conversations
- **AND** conversations within the focused agent view are ordered
  newest-first

#### Scenario: Unknown agents are hidden
- **GIVEN** the visible conversation set includes a conversation whose agent
  ID is not Claude, Codex, Cursor, or Antigravity
- **WHEN** the user opens the Conversations tab
- **THEN** that conversation is not rendered
- **AND** no `Other` section is shown

### Requirement: Empty agent sections are explicit

The Conversations TUI SHALL show a clear empty state for each known agent
section that has no visible conversations after the active filters are
applied when that agent section is focused.

#### Scenario: Known agent section has no conversations
- **GIVEN** the visible conversation set has Claude conversations and no
  Cursor conversations
- **WHEN** the user focuses the Cursor section
- **THEN** the Cursor section communicates that no Cursor conversations are
  available

#### Scenario: Project filter empties a section
- **GIVEN** multiple agent sections have conversations in the global view
- **WHEN** the user applies a project filter and focuses a known agent with
  no matching conversations
- **THEN** that agent section shows a filtered empty state

### Requirement: Row actions operate within the focused agent section

The Conversations TUI SHALL keep resume, delete, pending-delete
confirmation, and detail preview behavior scoped to the selected row in the
focused agent section.

#### Scenario: Resume selected conversation in focused section
- **GIVEN** the Codex section is focused and a Codex conversation row is
  selected
- **WHEN** the user presses Enter
- **THEN** ccmux resumes that Codex conversation using its conversation ID
  and agent-specific resume command

#### Scenario: Delete confirmation stays scoped to selected row
- **GIVEN** an agent section is focused and one of its rows is selected
- **WHEN** the user presses `x`
- **THEN** only that row is armed for deletion
- **AND** switching sections or moving rows disarms the pending delete

### Requirement: Keyboard navigation is section-scoped

The Conversations TUI SHALL keep up/down row movement inside the focused
agent section. The TUI SHALL switch focused agent sections with
Tab/Shift+Tab and left/right arrow keys.

#### Scenario: Up and down stay within a section
- **GIVEN** the Claude section is focused and its last row is selected
- **WHEN** the user presses Down
- **THEN** focus remains on the Claude section
- **AND** selection does not move into the next agent section

#### Scenario: User switches agent sections
- **GIVEN** the Claude section is focused
- **WHEN** the user presses Tab or Right
- **THEN** focus moves to the Codex section
- **WHEN** the user presses Shift+Tab or Left
- **THEN** focus moves back to the Claude section
