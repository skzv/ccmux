# ccmux demo tapes

VHS recordings of the 11 canonical CUJs. Each tape is hermetic — it
runs against a fully isolated environment (fake `$HOME`, isolated tmux
socket, stub agents) so renders are deterministic and never touch your
live sessions.

## Quick render

```bash
make build                          # fresh ccmux + ccmuxd binaries required
bash docs/vhs/render.sh <tape>      # renders one tape to docs/vhs/out/
make tapes                          # renders all tapes
```

C11 (update banner) needs an extra env var, which `make tapes` handles automatically:

```bash
CCMUX_UPDATE_DEMO=true bash docs/vhs/render.sh docs/vhs/cuj11_update.tape
```

## Dependencies

| Tool | Install | Required for |
|------|---------|--------------|
| `vhs` | `brew install vhs` | rendering tapes |
| `ffmpeg` | installed by vhs | GIF encoding |
| `tmux` | `brew install tmux` | session fixture |
| `git` | system | C11 update-check fixture |

## Tapes

| # | File | CUJ | What it shows |
|---|------|-----|---------------|
| C1 | `cuj01_new_project.tape` | Start a new project | Projects screen → `n` → form → session boots |
| C2 | `cuj02_dashboard.tape` | The dashboard | Sessions, status, usage panel, quota bar |
| C3 | `cuj03_attach_detach.tape` | Attach, work, detach | Enter attaches → Ctrl-b d detaches → TUI resumes |
| C4 | `cuj04_resume.tape` | Resume a conversation | Conversations screen → select past thread → Enter |
| C5 | `cuj05_pick_agent.tape` | Pick your agent | New-project form → cycle Claude / Codex / Antigravity / Cursor |
| C6 | `cuj06_notes.tape` | Project notes | Notes tab → Glamour preview → `/` ripgrep search |
| C7 | `cuj07_multi_machine.tape` | Multi-machine | Network screen with local + remote hosts |
| C8 | `cuj08_phone.tape` | Phone layout | Narrow-viewport (430px) dashboard |
| C9 | `cuj09_agents.tape` | Agents screen | Claude / Codex / Antigravity sub-tabs |
| C10 | `cuj10_setup_doctor.tape` | Setup & doctor | `ccmux doctor` health check → Settings screen |
| C11 | `cuj11_update.tape` | Update banner | Dashboard update banner → `ccmux update` |

## How isolation works

`render.sh` builds a hermetic world under a temp dir on every run:

1. **`$HOME`** → `$TMPDIR/ccmux-vhs.XXXXXX/home/` — fake home with config,
   projects, conversations, and notes pre-seeded.
2. **Tmux socket** → `$TMPDIR/ccmux-vhs.XXXXXX/tmux.sock` — a named socket
   never shared with the user's real tmux server. `unset TMUX` breaks parent-
   session inheritance so the user running inside tmux is safe.
3. **`tmux` wrapper** in `$root/bin/` prepends `-S $TMUX_SOCK` to every tmux
   call so `ccmuxd` and the TUI also hit the isolated socket.
4. **Stub agents** (`claude`, `codex`, `agy`) in `$root/bin/` print a plausible
   banner and `exec sleep 86400` — they look alive without running a real model.
5. **`cleanup()`** kills only the isolated socket server and the isolated ccmuxd;
   the user's real tmux sessions and daemon are never touched.

## Fixture contents

Seeded by `render.sh` for every tape:

- **Projects**: `auth-service` (Claude), `web-dashboard` (Codex), `ccmux`
- **Sessions**: one tmux session per project, running the relevant stub agent
- **Notes**: `README.md`, `docs/architecture.md`, `docs/api.md` in auth-service;
  `README.md` in web-dashboard (for C6)
- **Conversations**: 3 fake Claude JSONL transcripts across the two projects (for C4)
- **Claude config**: `~/.claude/CLAUDE.md` + two slash commands (for C9)
- **Git update repo**: `~/Projects/ccmux` is a git repo 1 commit behind its bare
  remote — enabled only when `CCMUX_UPDATE_DEMO=true` (C11)
- **Tour suppressed**: `tour.shown = true` in config so the first-run overlay
  doesn't appear in any tape

## Adding a new tape

1. Write `docs/vhs/cujNN_<slug>.tape` following the naming scheme.
2. Add it to the `TAPES` list in `Makefile` (the `make tapes` target).
3. Render and preview: `bash docs/vhs/render.sh docs/vhs/cujNN_<slug>.tape`
4. Commit both the tape file and the rendered GIF under `docs/vhs/out/`.

## Cross-repo asset flow

Rendered GIFs live in `docs/vhs/out/` (for the GitHub README).
The ccmux-website repo (`../ccmux-website`) needs copies under `public/demos/`
for the landing page and tutorial MDX. Copy manually after a re-render:

```bash
cp docs/vhs/out/cuj*.gif ../ccmux-website/public/demos/
```
