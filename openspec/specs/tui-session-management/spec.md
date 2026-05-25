# tui-session-management Specification

## Purpose
Define the TUI behavior for session lifecycle actions that can quit
ccmux or terminate managed tmux sessions.

## Requirements
### Requirement: Quit requests require confirmation
The TUI SHALL show a blocking confirmation modal when the user presses
`q` on a normal navigation surface where `q` would otherwise quit
ccmux. Confirming the modal SHALL exit only the ccmux TUI process and
MUST NOT terminate managed tmux sessions.

#### Scenario: Quit request opens confirmation
- **WHEN** the user presses `q` on a normal TUI navigation surface
- **THEN** ccmux displays a confirmation modal for quitting ccmux
- **THEN** ccmux remains running until the modal is confirmed

#### Scenario: Quit confirmation exits only ccmux
- **WHEN** the quit confirmation modal is open
- **THEN** confirming with `y`, Enter on the confirm action, or the mouse confirm action exits ccmux
- **THEN** managed tmux sessions remain running

#### Scenario: Quit cancellation preserves state
- **WHEN** the quit confirmation modal is open
- **THEN** cancelling with `n`, Esc, Enter on the cancel action, or the mouse cancel action dismisses the modal
- **THEN** the active screen, selection, focus, and managed sessions are unchanged

### Requirement: Session kill requests require confirmation
The TUI SHALL show a blocking confirmation modal when the user presses
`x` on the Sessions screen with a selected session. Confirming the modal
SHALL kill only the selected session captured when the modal opened.

#### Scenario: Kill request opens confirmation
- **WHEN** the user presses `x` on the Sessions screen with a selected session
- **THEN** ccmux displays a confirmation modal naming the selected session
- **THEN** the selected session remains running until the modal is confirmed

#### Scenario: Kill confirmation targets captured session
- **WHEN** the kill confirmation modal is open for a selected session
- **THEN** confirming with `y`, Enter on the confirm action, or the mouse confirm action kills that captured session
- **THEN** no other session is killed

#### Scenario: Kill cancellation preserves state
- **WHEN** the kill confirmation modal is open
- **THEN** cancelling with `n`, Esc, Enter on the cancel action, or the mouse cancel action dismisses the modal
- **THEN** the active screen, selected session, focus, and session list are unchanged

#### Scenario: Kill request without a selected session is ignored
- **WHEN** the user presses `x` on the Sessions screen and no session is selected
- **THEN** ccmux does not open a confirmation modal
- **THEN** ccmux does not issue a kill command

### Requirement: Confirmation modal blocks underlying actions
While a destructive-action confirmation modal is open, the modal SHALL
own keyboard and mouse input. Navigation keys, screen-switching keys,
refresh keys, attach keys, and destructive-action shortcuts MUST NOT
reach the underlying screen until the modal is dismissed.

#### Scenario: Underlying navigation is blocked
- **WHEN** a confirmation modal is open
- **THEN** pressing screen navigation, list navigation, refresh, attach, `q`, or `x` keys does not change the underlying TUI state
- **THEN** only modal confirm, cancel, and focus-move inputs affect the modal

#### Scenario: Arrow keys move modal focus
- **WHEN** a confirmation modal is open
- **THEN** Left/Right and Up/Down arrow keys move focus between cancel and confirm actions
- **THEN** pressing Enter activates the focused action

#### Scenario: Mouse can activate modal actions
- **WHEN** a confirmation modal is open and the user clicks a modal action
- **THEN** ccmux activates the clicked cancel or confirm action
