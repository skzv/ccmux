## Context

ccmux already distinguishes tmux mirror versus exclusive attach through
`sessions.attach_mode`. The config helper `DetachOthersOnAttach`
ultimately decides whether `tmux attach-session` gets `-d`.

That preference is correct for explicit "attach to an existing session"
actions, where the user may intentionally want exclusive control. It is
too broad for create-then-attach flows. When ccmux just created a new
session, attaching with `-d` can detach other terminal clients on the
same tmux server even though the user only asked to open one additional
session.

The current call sites mix these concepts:

- Local TUI attach routes through `localAttachCmd`, which always reads
  `a.cfg.Sessions.DetachOthersOnAttach()`.
- Remote TUI create results route through `remoteSessionStartedMsg`,
  whose handler builds `remoteTmuxAttach(..., a.cfg.Sessions.DetachOthersOnAttach())`.
- `ccmux shell` is documented as the CLI equivalent of the Sessions tab
  `n` flow but hardcodes `tmux attach-session -d` locally and remotely.

## Goals / Non-Goals

**Goals:**

- Newly created sessions attach without `-d` for local and remote
  Sessions tab flows.
- `ccmux shell` preserves the same no-detach behavior because it is the
  CLI mirror of the bare-session creation workflow.
- Explicit attaches to existing sessions continue to honor
  `sessions.attach_mode`.
- Nested-tmux create-then-attach keeps using `switch-client`.

**Non-Goals:**

- Changing the meaning or default of `sessions.attach_mode`.
- Changing tmux session creation semantics, naming, agent launch command
  resolution, or remote daemon APIs.
- Reworking the attach-loading overlay or remote transport selection.

## Decisions

1. Treat attach mode as a property of the attach intent, not just user
   config.

   Create-then-attach call sites should pass an explicit no-detach value
   into the attach command builder. Existing-session attach call sites
   should keep using `a.cfg.Sessions.DetachOthersOnAttach()`.

   Alternative considered: changing the global config helper to return
   mirror mode while creating sessions. That would hide the intent at
   call sites and risk breaking explicit attach behavior.

2. Add a small local attach helper or parameterized path instead of
   duplicating attach execution.

   The implementation should keep the current `prepLocalAttachCmd` and
   `attachReadyMsg` pipeline so Moshi detection, tmux chrome, the loading
   overlay, and nested-tmux `switch-client` handling remain centralized.
   The only difference for new sessions should be the `DetachOthers`
   value carried into `attachReadyMsg`.

   Alternative considered: bypassing `localAttachCmd` for new sessions
   and directly executing `tmux attach-session`. That would regress the
   nested-tmux behavior this code path already fixed.

3. Keep remote command construction centralized.

   Remote create-then-attach should call the same remote attach command
   builder with `detachOthers=false`. Explicit remote attaches should
   continue passing the configured attach preference.

   Alternative considered: adding a separate remote command builder just
   for new sessions. That would duplicate shell quoting and PATH-prefix
   behavior without changing the actual command shape.

## Risks / Trade-offs

- A create flow that sometimes returns an existing session could now
  mirror rather than detach exclusively. This is acceptable for this
  change because the user action is still "create/open a new session",
  and preserving existing clients is the safer behavior.
- Tests cannot perform a fully interactive attach without a controlling
  terminal. Mitigation: assert command construction and message state at
  the attach boundary, matching the existing attach test strategy.
