## Why

ccmux currently lets a user quit the TUI with `q` and kill the
selected session with `x` from the Sessions screen. Both keys are fast
single-keystroke actions, and `x` is destructive: a stray keypress can
terminate a live tmux session.

Users need a deliberate confirmation step before leaving ccmux or
killing a session, while preserving the speed of the existing keyboard
workflow for intentional actions.

## What Changes

- Add a blocking in-TUI confirmation modal for `q` quit requests on
  normal navigation surfaces.
- Add a blocking in-TUI confirmation modal for `x` kill requests on the
  Sessions screen when a session is selected.
- Make cancellation the safe path: `n`, Esc, and the modal cancel action
  dismiss the modal without changing screen, selection, focus, or
  session state.
- Allow confirmation with `y`, Enter, and the modal confirm action.
- Allow arrow keys and mouse interaction to move between or activate the
  modal's confirm/cancel actions.
- Keep `q` quit scoped to exiting ccmux only; it must not terminate
  managed tmux sessions.
- Keep `x` kill scoped to the currently selected session only; it must
  not affect any other session.
- Leave confirmation always enabled with no persisted setting.
- Add focused model/unit tests plus sandboxed e2e coverage for cancel
  and confirm paths.

## Capabilities

### New Capabilities

- `tui-session-management`: ccmux TUI session lifecycle actions,
  including guarded quit and kill behavior.

### Modified Capabilities

- `cuj-e2e-coverage`: The e2e suite must verify destructive TUI
  confirmations in a sandboxed tmux/home/config environment.

## Impact

- `internal/tui` — key routing, confirmation modal model/view, session
  kill dispatch, quit dispatch, focus handling, mouse/arrow handling.
- `internal/e2e` — sandboxed PTY-driven TUI tests for kill and quit
  confirmation flows.
- Tests — focused Go tests for confirm/cancel keyboard behavior and
  state preservation.
- No new config, persistence, external services, or dependencies are
  expected.
