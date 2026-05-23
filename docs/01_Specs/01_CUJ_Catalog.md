# ccmux — CUJ Catalog

The canonical list of ccmux's load-bearing **critical user journeys**.
Every journey listed here has a tape under `docs/vhs/` and (for entries
marked `demoable: full`) a rendered GIF under `docs/vhs/out/`. `make
tapes-check` parses this file to enforce the invariant — adding a CUJ
here without its tape (or vice versa) breaks the check.

**Demoable classifications:**

- **`full`** — capturable in a single VHS terminal recording, no faked
  hosts or agents. The dashboard, attach, conversations, notes, etc.
- **`stubbed`** — only capturable with faked tailnet peers or other
  external state. Currently just C7 (the Network screen exposes live
  Tailscale peer names/IPs, so the GIF is gitignored until `render.sh`
  can mask the tailnet — the tape stays, the GIF doesn't).
- **`still`** — not capturable in motion; ships as a screenshot only.

The mapping from catalog ID to artifacts is mechanical:
`Cn` → `docs/vhs/cuj{NN}_{slug}.tape` (and matching `.gif` under
`docs/vhs/out/` when `demoable: full`), where `NN` is the zero-padded
ID number.

## Journeys

<!-- BEGIN_CATALOG (parsed by `make tapes-check`) -->

| ID  | Slug            | Name                       | Description                                                      | Value                                                                  | Demoable |
| --- | --------------- | -------------------------- | ---------------------------------------------------------------- | ---------------------------------------------------------------------- | -------- |
| C0  | hero            | Hero tour                  | Five parallel agents, attach to real Claude, tour every screen   | The one-take pitch — "this is what ccmux does"                         | full     |
| C1  | new_project     | New project from scratch   | Create a project, scaffold the layout, launch the chosen agent   | Zero-to-live-session in a single form                                  | full     |
| C2  | dashboard       | Dashboard at a glance      | Sessions × hosts × agents in one pane, color-coded by state      | See every machine and every agent's state without context-switching    | full     |
| C3  | attach_detach   | Attach and detach          | Enter to drop into the agent, Ctrl-b d to release                | One key in, one key out — tmux is the session store, ccmux is the lens | full     |
| C4  | resume          | Resume a conversation      | Pick a past Claude/Codex/agy thread, Enter resumes the daemon    | The "ccmux resume" affordance, every device                            | full     |
| C5  | pick_agent      | Pick the agent             | Cycle Claude → Codex → Antigravity per project; choice persists  | One TUI drives whichever agent you're actually paying for              | full     |
| C6  | notes           | Project notes              | Markdown tree, Glamour preview, ripgrep-backed search            | Terminal-native notes without a sync service                           | full     |
| C7  | multi_machine   | Multi-machine over tailnet | Auto-discovered ccmuxd peers, attach across the tailnet          | Your laptop, your Mac mini, your phone — one dashboard                 | stubbed  |
| C8  | phone           | Phone-width TUI            | 430-px terminal collapses to a single-column dashboard           | Mobile-first without (yet) needing a native app                        | full     |
| C9  | agents          | Manage agents              | Per-agent CLI, config root, login status, command palette        | One screen for "is my agent installed and signed in?"                  | full     |
| C10 | setup_doctor    | Setup wizard / doctor      | Interactive wizard installs deps; doctor diagnoses a stuck setup | Friendly first-run on any machine; one command fixes a broken host     | full     |
| C11 | update          | In-place updates           | Banner appears, ccmux re-installs itself, daemon restarts        | The TUI never lets you forget you're on a stale build                  | full     |

<!-- END_CATALOG -->

## Adding a new journey

1. Append a row to the table above (next free `Cn`).
2. Record the tape at `docs/vhs/cuj{NN}_{slug}.tape`.
3. Render the GIF (`docs/vhs/render.sh docs/vhs/cuj{NN}_{slug}.tape`).
4. `make tapes-check` should now pass.

`stubbed` entries skip the GIF requirement so the tape can live in the
repo while the rendering blocker (faked tailnet for C7, etc.) is worked
on separately.
