# Muse acceptance evidence

Installed Muse Code 1.0.3 (1.0.3-R2198.1) from the official Homebrew cask.
Authenticated Meta inference passed on 2026-09-08 after the account plan upgrade.
The native model was muse-spark-1.3-contributor; native OAuth stayed in Keychain.

## Native acceptance

- A fresh paid request replied “Muse is ready for ccmux.” on its first attempt.
- Exact UUID resume restored that conversation. A second request spawned a
  native helper subagent, which replied READY; the parent then replied DONE.
- ccmux read four parent messages and two log paths. Five unique native model
  completions totaled 124,787 input, 791 output, 67,795 cached input and 449
  reasoning tokens. Child usage was included without adding child messages to
  the parent preview or double-counting attribution records.
- Deletion refused the running native process. After stopping that private tmux
  session, deletion removed the parent directory, child logs and view cache.
- Resumed conversations now retain their own agent identity even when the
  workspace default is another agent. Native tmux and daemon tests cover this.

## Validation

- `go test ./...`, `make lint`, `go vet ./...`, `make test-e2e`.
- Race tests for Muse, agents, conversations, usage, tmux and the daemon poll loop.
- `make fuzz-quick` (100,000 executions per registered target).
- `CCMUX_NATIVE_MUSE=1 go test ./internal/muse -run TestInstalledMuseLifecycle -v`:
  reproducible native echo lifecycle, export identity and turn count, exact
  resume, live lock rejection, and complete removal after exit.
- Website check/build, no missing translations, static checks across 171 pages,
  and 90 desktop/mobile browser tests across nine languages.

Fixtures cover duplicate completion/attribution records, child usage, partial
writes, Unicode metadata search and active parent/child deletion. Native states
include workspace trust, empty/populated input, Thinking, plan-required and
model errors. No billing settings were changed by the integration.

Final authenticated launch media and delivery verification are recorded below
as they complete. X posts remain drafts and have not been published.

## Launch media

Both VHS tapes were regenerated with the authenticated Meta provider. Reviewed
all three screenshots and video transitions: complete responses, exact native
resume, clean status labels, readable panels and no billing/auth errors. MP4s
are H.264/yuv420p at 1600×960, lasting 25.48s and 24.16s; GIFs also decode.
The capture harness cleaned up its private daemon and tmux processes.
