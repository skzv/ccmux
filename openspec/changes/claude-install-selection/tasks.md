## 1. Spec

- [x] 1.1 Create OpenSpec proposal for deterministic agent install selection.
- [x] 1.2 Define capability requirements for configured agent command resolution.

## 2. Implementation

- [x] 2.1 Add config shape for persisted agent command paths.
- [x] 2.2 Add agent helpers to enumerate executable candidates and render launch/resume commands with configured commands.
- [x] 2.3 Update setup wizard to prompt when multiple installations are detected for supported agents.
- [x] 2.4 Update daemon service generation so configured command directories are included in service `PATH`.
- [x] 2.5 Route project, bare-session, and conversation-resume launches through configured commands.
- [x] 2.6 Extend doctor output for multiple Claude installations and configured command mismatch visibility.

## 3. Verification

- [x] 3.1 Add/update unit tests for config, agent command resolution, service PATH, setup wizard, daemon launch helpers, scaffold launch helpers, and conversation resume helpers.
- [x] 3.2 Run focused tests for touched packages.
- [x] 3.3 Run `openspec validate claude-install-selection --no-interactive`.
