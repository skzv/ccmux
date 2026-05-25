## ADDED Requirements

### Requirement: Destructive TUI confirmations are verified end to end
The e2e suite SHALL verify the TUI confirmation flows for quitting ccmux
and killing a session against the real ccmux binary in a sandboxed
environment. The tests MUST isolate `$HOME`, ccmux config, sockets, and
tmux state from the developer's real environment.

#### Scenario: Session kill cancel is sandboxed and non-destructive
- **WHEN** the e2e harness starts a sandboxed tmux session and drives the TUI to press `x`
- **THEN** the TUI displays the kill confirmation modal
- **THEN** cancelling the modal leaves the sandboxed session running
- **THEN** no developer tmux sessions or config are read or mutated

#### Scenario: Session kill confirm removes only the sandboxed target
- **WHEN** the e2e harness starts multiple sandboxed tmux sessions and confirms killing the selected one from the TUI
- **THEN** the selected sandboxed session is removed
- **THEN** the other sandboxed sessions remain running

#### Scenario: Quit cancel keeps the TUI running
- **WHEN** the e2e harness drives the TUI to press `q`
- **THEN** the TUI displays the quit confirmation modal
- **THEN** cancelling the modal leaves the TUI process running and interactive

#### Scenario: Quit confirm exits without killing sessions
- **WHEN** the e2e harness starts a sandboxed tmux session and confirms quitting ccmux from the TUI
- **THEN** the TUI process exits
- **THEN** the sandboxed tmux session remains running until harness cleanup
