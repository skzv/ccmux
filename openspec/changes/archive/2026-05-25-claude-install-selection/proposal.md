## Why

ccmux can see different agent binaries depending on which process is
launching the session. An interactive shell may prefer an npm/NVM
install, while the launchd/systemd-managed `ccmuxd` service may have a
fixed service `PATH` that prefers Homebrew. When multiple Claude Code,
Codex, or Antigravity CLI installations are present, setup currently
reports only whether the binary exists and daemon-started sessions can
silently use a different installation than the one the user expects.

## What Changes

- Detect every supported agent executable visible on `PATH`,
  preserving resolution order.
- During the AI agents section of `ccmux setup`, when multiple
  installations are found for a supported agent, ask which one ccmux
  should use and persist the selected executable in
  `~/.config/ccmux/config.toml` before continuing to optional installs
  and later setup steps.
- Make agent session launches and conversation resumes honor the
  persisted command when present.
- Regenerate daemon service environment with configured agent command
  directories before Homebrew/system paths, so daemon-spawned sessions
  and foreground-spawned sessions resolve consistently.
- Extend `ccmux doctor` output so multi-installation state is visible
  for supported agents: configured command, PATH-first command, and all
  discovered candidates.

## Capabilities

### New Capabilities

- `agent-install-resolution`: ccmux deterministically resolves which
  executable it will use for an agent when multiple installations exist.

### Modified Capabilities

- `multi-agent`: Agent launches may use a configured command path
  instead of the first binary on the current process `PATH`.
- `onboarding`: setup and doctor surface multiple Claude installations
  and guide the user to a stable selection.

## Impact

- `internal/agent` — command candidate discovery, configured launch
  command rendering, and resume argv substitution.
- `internal/config` — persisted per-agent command setting.
- `internal/setupwizard` — setup-time agent install selector.
- `internal/daemonservice` — daemon service `PATH` includes configured
  command directories.
- `cmd/ccmuxd`, `cmd/ccmux`, `internal/tui`, `internal/scaffold` —
  session launch/resume sites use configured commands.
- `cmd/ccmux doctor` — diagnostics for multiple Claude installs.
- Tests for command discovery, command rendering, config round-trip,
  service PATH generation, setup selection behavior, and launch command
  propagation.
