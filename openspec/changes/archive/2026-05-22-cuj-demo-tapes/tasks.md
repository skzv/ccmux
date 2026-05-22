## 1. CUJ catalog

- [ ] 1.1 Create `docs/01_Specs/04_CUJ_Catalog.md` with the C1–C11 table from `proposal.md` — ID, journey name, description, value statement, and `full`/`stubbed`/`still` demoable classification per CUJ.
- [ ] 1.2 Add `docs/01_Specs/04_CUJ_Catalog.md` to the Docs Map in `CLAUDE.md`.
- [ ] 1.3 Cross-link the catalog from `docs/vhs/README.md` so the tape set and the catalog reference each other.

## 2. Hermetic render harness

- [ ] 2.1 Write `docs/vhs/fixture.sh` — creates a temp `$HOME` + `TMUX_TMPDIR`, writes stub `claude`/`codex`/`agy` agents (echo marker + `sleep`) and a stub `mosh` onto a `PATH` dir, seeds 2–3 fake projects, a fixed `config.toml`, and a couple of pre-canned `~/.claude/projects/*.jsonl` transcripts.
- [ ] 2.2 Write `docs/vhs/netsim.sh` — stand up N `ccmuxd` instances on distinct loopback addresses (`127.0.0.2`, `127.0.0.3`, …), each with its own isolated fake `$HOME`/`TMUX_TMPDIR` and stub-agent sessions, registered in `hosts.toml` as `mac-mini` / `linux-box`; teardown kills every spawned daemon and removes the temp trees. Built reusable for a future multi-host e2e test.
- [ ] 2.3 Write `docs/vhs/render.sh` — for a given tape: check `vhs` is installed (print `brew install vhs` hint if not), source `fixture.sh` (and `netsim.sh` for the C7 tape), run `vhs <tape>`, tear everything down.
- [ ] 2.4 Add `make tapes` (render every `docs/vhs/*.tape` via `render.sh`) and `make tapes-check` (assert every `full`/`stubbed` catalog CUJ has a `cujNN_*.tape`; exit non-zero naming gaps) to the root `Makefile`.
- [ ] 2.5 Wire `make tapes-check` into the CI `test` job in `.github/workflows/ci.yml` (no VHS needed — pure file-existence check).

## 3. Tapes — headline & lifecycle CUJs (all TUI-driven)

- [ ] 3.1 Re-author the three legacy tapes into the scheme: `02_dashboard`→`cuj02_dashboard` (already TUI; just make it hermetic); `01_new_project`→`cuj01_new_project` and `03_update`→`cuj11_update` (today bare-CLI — rewrite as TUI recordings). Each emits both `.gif` and `.webp`.
- [ ] 3.2 Author `cuj01_new_project.tape` — Projects screen, `n` opens the new-project form, fill name + agent + first prompt, ccmux creates the dir and boots the session. **Depends on the scaffolding-removal change having landed.**
- [ ] 3.3 Author `cuj03_attach_detach.tape` — `Enter` to attach a session, work, `Ctrl-b d` to detach back to the dashboard.
- [ ] 3.4 Author `cuj04_resume_conversation.tape` — open a project, pick a past conversation from the project menu, resume it.
- [ ] 3.5 Author `cuj05_pick_agent.tape` — choose Claude / Codex / Antigravity for a project and boot that agent.
- [ ] 3.6 Author `cuj11_update.tape` — the dashboard's update banner; check for and apply an update from within the TUI.

## 4. Tapes — notes, config, setup

- [ ] 4.1 Author `cuj06_notes.tape` — Notes tab: browse the project markdown tree, Glamour preview, `/` ripgrep search (browse/preview/search only — the templated note quick-actions are removed by the scaffolding-removal change).
- [ ] 4.2 Author `cuj09_agent_config.tape` — the Agents screen: switch model, browse commands/skills.
- [ ] 4.3 Author `cuj10_setup_doctor.tape` — the Setup screen (dependency checks, SSH key, QR) and the doctor health view, driven through the TUI.

## 5. Tapes — multi-machine & mobile (stubbed)

- [ ] 5.1 Author `cuj07_multi_machine.tape` — source `netsim.sh`, show the TUI dashboard listing the local host plus `mac-mini` / `linux-box` color-coded, attach a remote session via the stub `mosh`.
- [ ] 5.2 Author `cuj08_phone.tape` — `Set Width`/`Set Height` to an iPhone-portrait viewport, record the narrow-layout TUI, end on the visible "waiting for input" marker.
- [ ] 5.3 Reclassify any CUJ that cannot be shown honestly as `still` and commit an annotated screenshot instead (none expected — verify).

## 6. Render & surface in ccmux

- [ ] 6.1 Run `make tapes`; commit the rendered GIF + WebP assets to `docs/vhs/out/`.
- [ ] 6.2 Replace the `<!-- DEMO_GIF -->` placeholder in `README.md` with the C1 + C2 headline GIFs.
- [ ] 6.3 Add a "Demos" gallery section to `README.md` linking the remaining CUJ demos.
- [ ] 6.4 Run `make tapes-check` and `go test ./...`; confirm both pass.

## 7. Surface on ccmux.ai (sister repo `../ccmux-website`)

- [ ] 7.1 Copy the rendered `.webp` demos into `../ccmux-website/public/demos/`.
- [ ] 7.2 Add a hero demo + CUJ showcase strip to `../ccmux-website/src/pages/index.astro`.
- [ ] 7.3 Embed each CUJ's demo in its matching tutorial MDX under `../ccmux-website/src/content/docs/tutorials/` (01↔C1, 02↔C2, 03↔C8, 04↔C5, 05↔C7, 06↔C11).
- [ ] 7.4 `npm run build` + `npx astro check` in `../ccmux-website`; open a separate PR in that repo.

## 8. Verification

- [ ] 8.1 Verify every `full`/`stubbed` catalog CUJ has a rendered GIF + WebP and a tape (`make tapes-check` green).
- [ ] 8.2 Verify the README renders the headline demos on a fresh clone (no build step needed).
- [ ] 8.3 Verify each mapped ccmux.ai tutorial page embeds its demo in the built site.
