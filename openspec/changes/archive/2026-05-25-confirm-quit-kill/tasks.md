## 1. OpenSpec

- [x] 1.1 Validate the `confirm-quit-kill` OpenSpec change before implementation.

## 2. Confirmation Modal

- [x] 2.1 Add App-level destructive-action confirmation state for quit and selected-session kill.
- [x] 2.2 Render a blocking confirmation modal that names the action and target, defaults focus to cancel, and fits existing TUI styles.
- [x] 2.3 Route `y`, `n`, Esc, Enter, arrow keys, and mouse events while the modal is open without leaking input to the underlying screen.

## 3. Quit And Kill Behavior

- [x] 3.1 Change normal-surface `q` handling to open the quit confirmation modal and confirm by exiting only ccmux.
- [x] 3.2 Change Sessions-screen `x` handling to open the kill confirmation modal and confirm by killing only the captured selected session.
- [x] 3.3 Ensure cancel paths preserve active screen, selected session, focus, and tmux session state.
- [x] 3.4 Ensure `x` with no selected session remains a no-op.

## 4. Tests

- [x] 4.1 Add focused TUI model tests for quit confirm/cancel keyboard behavior.
- [x] 4.2 Add focused TUI model tests for kill confirm/cancel behavior, captured-session targeting, and no-selection no-op behavior.
- [x] 4.3 Add focused tests for arrow-key focus movement and modal input blocking.
- [x] 4.4 Add sandboxed e2e tests for kill cancel, kill confirm, quit cancel, and quit confirm preserving managed sessions.

## 5. Verification

- [x] 5.1 Run `openspec validate confirm-quit-kill --type change --strict --no-interactive`.
- [x] 5.2 Run focused Go tests for `internal/tui`.
- [x] 5.3 Run the relevant sandboxed e2e tests with the integration tag or `make test-e2e` when practical.
