## Why

Creating a new ccmux session should not disturb any tmux clients the
user already has open. Today, several create-then-attach paths reuse the
general attach preference (`sessions.attach_mode`). When that preference
is exclusive, ccmux attaches with `tmux attach-session -d`, which
detaches other clients from the target tmux server. From the user's
perspective, making a new session can close or blank existing terminal
tabs even though those tabs were not part of the new session.

New-session creation needs a narrower contract: create the new tmux
session, attach the current client to it, and leave every existing tmux
client alone.

## What Changes

- Make create-then-attach flows attach in mirror mode, omitting tmux's
  `-d` detach flag regardless of `sessions.attach_mode`.
- Cover the Sessions tab `n` flow for both local and remote hosts.
- Keep `ccmux shell` aligned with the Sessions tab bare-session flow so
  its local and remote attach commands also preserve existing clients.
- Keep existing-session attach behavior configurable through
  `sessions.attach_mode`; this change only changes attach behavior after
  ccmux has just created a new session.
- Keep nested-tmux behavior intact: when ccmux is already inside tmux,
  the create-then-attach flow must still use `switch-client` rather than
  attempting a nested `attach-session`.

## Capabilities

### New Capabilities

- `session-attach-preservation`: Newly created sessions are joined
  without detaching other tmux clients.

### Modified Capabilities
<!-- Existing capabilities whose REQUIREMENTS are changing (not just implementation).
     Only list here if spec-level behavior changes. Each needs a delta spec file.
     Use existing spec names from openspec/specs/. Leave empty if no requirement changes. -->

None.

## Impact

- `internal/tui`: create-then-attach message handling and local/remote
  attach command construction.
- `cmd/ccmux/cmd`: `ccmux shell` local and remote attach commands.
- `internal/tmux`: existing attach argument helpers may be reused to
  keep mirror/exclusive command construction consistent.
- Tests for the Sessions tab local and remote new-session paths, CLI
  shell attach command construction, and existing-session attach
  behavior remaining configurable.
