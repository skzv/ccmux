## 1. OpenSpec

- [x] 1.1 Complete OpenSpec proposal, design, capability spec, and this checklist for Grok agent support, and run `openspec validate add-grok-agent`.

## 2. Agent Model

- [x] 2.1 Add `internal/agent/grok.go` implementing the `Agent` interface: ID `grok`, display name `Grok`, binary `grok`, `LaunchCmd` (`grok` / `grok --continue || grok || zsh || bash || sh`), `ConfigRoot` = `~/.grok`, `TranscriptsRoot` = `~/.grok/sessions`, `AGENTS.md`-centered `InitialPrompt`, conservative `Classify`.
- [x] 2.2 Register Grok in `agent.go`: add `IDGrok` const, append `Grok{}` to `All()`, add cases to `ByID` and `ParseID`, and add the `grok --resume <id>` branch to `ResumeArgs`.
- [x] 2.3 Add `Commands.Grok`, plus `grok` cases in `commandOverride` and `configuredBinary`, so configured-command substitution flows through launch/resume.
- [x] 2.4 Add focused unit tests in `internal/agent` (parse/registry order/launch/resume/configured-command), and extend the fuzz `ParseID` corpus to cover `grok`.

## 3. Config & Daemon

- [x] 3.1 Add the persisted `agents.grok.command` setting to `internal/config` and its conversion into `agent.Commands.Grok`; cover round-trip with a test.
- [x] 3.2 Confirm `internal/daemonservice` PATH generation includes a configured Grok command directory; extended `TestManagedPath_*` to assert the Grok bin dir.

## 4. Product Surfaces

- [x] 4.1 Update `internal/setupwizard` to include Grok install hint (`curl -fsSL https://x.ai/cli/install.sh | bash` or `npm install -g @xai-official/grok`) and multi-binary command selection.
- [x] 4.2 Update `cmd/ccmux/cmd` `--agent` help/error text (new/shell/resume) and `ccmux doctor` agent diagnostics to include Grok; doctor_test iterates `All()` so Grok's install hint is exercised.
- [x] 4.3 Update `internal/tui` agent picker/labels/styles (Grok=Blue accent), Agents-screen subtab body, `nextAgent` wrap test, and regenerate Projects-screen legend goldens (five → six).

## 5. Docs

- [x] 5.1 Update README and `docs/01_Specs/02_Multi_Agent.md` (and any agent table) to list Grok.
- [ ] 5.2 Update the website docs/MDX where the supported-agent set is user-visible. DEFERRED: `../ccmux-website` is a separate repo with its own deploy, and its agent lists are already stale (only "Claude / Codex / Antigravity" — missing Cursor *and* pi). Backfilling Cursor + pi + Grok consistently and deploying is its own website-repo change; needs user sign-off before touching/deploying that repo.

## 6. Verification

- [x] 6.1 Ran `go test ./...` (clean), `gofmt -l` (clean), `go vet ./...` (clean), cross-compiled GOOS=linux + GOOS=windows (both build), and fuzzed FuzzParseID 5s (no crashers).
- [x] 6.2 Verified against a real grok 0.2.3 install: `ConfigRoot=~/.grok` ✓, `TranscriptsRoot=~/.grok/sessions` ✓, `-c/--continue` + `-r/--resume [ID]` ✓, AGENTS.md read (confirmed in grok's system prompt) ✓. `ccmux doctor` detects Grok; a tmux launch of `grok --continue` resumed the cwd session and rendered the Grok TUI. Session on-disk format = per-cwd/`<uuidv7>` dirs (`chat_history.jsonl` + siblings) + `session_search.sqlite`; future `ListGrok` should shell `grok sessions`/`grok export` rather than parse files.
