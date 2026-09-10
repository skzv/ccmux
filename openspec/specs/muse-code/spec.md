# muse-code Specification

## Purpose
Integrate native Meta Muse Code sessions into ccmux so users can launch, monitor, resume, inspect usage and safely remove conversations through both the terminal UI and CLI.

## Requirements
### Requirement: Muse session control
ccmux SHALL expose Muse Code through CLI and TUI selection, setup and monitoring with custom executable support and native fresh/latest/exact resume commands.
#### Scenario: Resume a selected conversation
- **WHEN** a user resumes a Muse history entry
- **THEN** ccmux launches the configured Muse executable with `resume` and that session UUID in its workspace.

### Requirement: Native history and usage
ccmux SHALL read Muse native logs, show messages and grouped parent history, and count model usage once across mirrored records and children without inventing cost.
#### Scenario: Mirrored completion events
- **WHEN** a model completion also appears in attribution or duplicate records
- **THEN** its tokens are counted once and the user turn appears once.

### Requirement: Guarded complete deletion
ccmux SHALL delete the selected inactive Muse session and corresponding derived cache through existing confirmation surfaces and reject active, symlinked or out-of-root targets.
#### Scenario: Session is active
- **WHEN** deletion targets a locked Muse session
- **THEN** deletion fails without removing any session data.

### Requirement: Tested release and launch assets
The change SHALL ship translated documentation, validated release artifacts and three English X drafts with reviewed screenshots and recordings.
#### Scenario: Release acceptance
- **WHEN** the release is published
- **THEN** native Muse integration, Homebrew installation, website checks and media playback have passed and X posts remain unpublished drafts.

