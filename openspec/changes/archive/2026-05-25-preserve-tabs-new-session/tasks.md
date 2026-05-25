## 1. Spec

- [x] 1.1 Create OpenSpec proposal for preserving existing tmux clients during new-session attach.
- [x] 1.2 Define session attach preservation requirements for local, remote, CLI, and nested-tmux create flows.

## 2. Implementation

- [x] 2.1 Add or adjust local attach plumbing so create-then-attach callers can force `detachOthers=false` while existing-session attach callers keep using config.
- [x] 2.2 Update the local Sessions tab bare-session completion path to attach without detaching other clients.
- [x] 2.3 Update the remote Sessions tab bare-session completion path to build ssh/mosh attach commands without `-d`.
- [x] 2.4 Update `ccmux shell` local and remote attach commands to omit `-d` after creating a new bare session.
- [x] 2.5 Audit other create-then-attach call sites and use no-detach attach only where the user action creates a new session.

## 3. Verification

- [x] 3.1 Add or update unit tests proving new-session attach paths omit `-d` even when config requests exclusive attach.
- [x] 3.2 Add or update unit tests proving explicit existing-session attach still honors mirror and exclusive config.
- [x] 3.3 Run focused tests for touched packages.
- [x] 3.4 Run `openspec validate preserve-tabs-new-session --type change --strict --no-interactive`.
