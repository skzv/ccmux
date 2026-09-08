# Muse acceptance evidence

Installed Muse Code 1.0.3 (1.0.3-R2198.1) from the official Homebrew cask.
Native command help, echo-mode logs, export and live locks were inspected.

Passed locally:

- `go test ./...`, `make lint`, `make test-e2e`.
- Race tests for Muse, agents, conversations and usage.
- `make fuzz-quick` (100,000 executions per registered target).
- `CCMUX_NATIVE_MUSE=1 go test ./internal/muse -run TestInstalledMuseLifecycle -v`:
  native exec writes a session; ccmux history matches native export identity and
  turn count; exact UUID resume adds a second user/assistant turn; deletion
  refuses the running native process, then removes session and cache after exit.
- Website check/build, no missing translations, static checks across 171 pages,
  and 90 desktop/mobile browser tests across nine languages.

The native echo backend is used for the reproducible lifecycle test. It does not
produce paid-model token counts or prove authenticated Meta inference.

Authenticated Meta acceptance remains pending: login completed, but both native
headless and interactive requests return HTTP 402. The final interactive error is
“Billing verification failed. Please check your payment method.” The installed
CLI points to https://accountscenter.meta.com/muse_code/?ep=no_payg.
No billing settings were changed.

Captured native states include workspace trust, empty and populated input,
timed Thinking with provider retries, the plan-required banner, and model error.
Unit fixtures cover duplicate completion/attribution records, child usage,
partial log writes, Unicode metadata search, and active parent/child deletion.

Do not mark release/media acceptance complete until a Meta-backed response and
its usage are verified. Rerender and inspect both launch tapes with provider meta
before replacing the offline previews. X posts remain local drafts.
