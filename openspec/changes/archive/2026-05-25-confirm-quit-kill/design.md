## Context

The ccmux TUI uses Bubble Tea and routes global keys through
`internal/tui.App.Update`. Today `q` exits immediately from normal
navigation surfaces, while `x` on the Sessions screen is handled by
`sessionsModel.Update` and immediately returns a `killSessionCmd` for
the selected session.

ccmux already has modal patterns for help, new-session, rename, project
menus, and pickers. This change needs a small confirmation modal that
blocks other navigation/actions until the user confirms or cancels.

## Goals / Non-Goals

**Goals:**

- Require confirmation before `q` exits ccmux from normal navigation
  surfaces.
- Require confirmation before `x` kills the selected Sessions row.
- Make cancel safe and state-preserving.
- Support keyboard, arrow-key focus movement, and mouse interaction in
  the modal.
- Verify behavior with focused model tests and sandboxed e2e tests.

**Non-Goals:**

- Add configuration or persistence for disabling confirmations.
- Change CLI commands such as `ccmux kill`.
- Add bulk or multi-select session kill behavior.
- Terminate sessions when confirming `q`; quit only exits ccmux.
- Change text-entry modal semantics where `q` is currently typed into
  the field instead of acting as a global quit.
- Change Ctrl-C emergency quit behavior unless implementation discovers
  it is already coupled to the same quit path and tests explicitly pin
  the chosen behavior.

## Decisions

1. Use an App-level confirmation modal for destructive actions. The
   modal should sit above screen models so it can guard global quit and
   Sessions kill consistently, and so key/mouse handling cannot leak to
   the underlying screen while the modal is open.

2. Keep the modal action-specific instead of adding a generic framework.
   A small confirmation state with kind, title/body, selected button,
   and target session name is enough for quit/kill and avoids broad UI
   refactoring.

3. Move Sessions kill initiation out of immediate command dispatch. The
   first `x` on a selected session should open the modal; only the
   confirm action should run `killSessionCmd` for the captured selected
   session name.

4. Capture the session name when opening the kill modal. Refreshes or
   selection movement while the modal is open must not silently retarget
   the kill. Because the modal blocks underlying navigation, cancel
   returns the user to the same selection/focus state.

5. Make cancel the default focused action. Enter should activate the
   focused action, `y` should confirm directly, `n` and Esc should
   cancel directly, and arrow keys should move focus between cancel and
   confirm. Mouse click/selection should activate the intended action
   when Bubble Tea mouse events are enabled for the TUI.

6. Keep e2e tests hermetic. Tests should reuse the existing integration
   harness so `$HOME`, config, tmux server, sockets, and session names
   stay isolated from the developer's real environment.

## Risks / Trade-offs

- Mouse event support may require enabling Bubble Tea mouse mode for
  the TUI process. Mitigation: keep handling scoped to the confirmation
  modal and assert that existing keyboard flows still work.
- If quit and kill confirmation are implemented in separate screen
  models, routing can diverge. Mitigation: prefer one App-level
  confirmation state and tests that cover both actions.
- PTY e2e tests for process exit can be timing-sensitive. Mitigation:
  assert observable modal text, process liveness after cancel, and
  process exit after confirm using the existing bounded harness waits.
