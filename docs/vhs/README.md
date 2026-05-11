# VHS tapes

[VHS](https://github.com/charmbracelet/vhs) is a declarative recorder for terminal sessions — you describe keystrokes and pauses in a `.tape` file and `vhs file.tape` renders a GIF.

Render any of these locally:

```bash
brew install vhs
vhs docs/vhs/01_new_project.tape
# → out/01_new_project.gif
```

Then drop the GIF into the README (replace the `<!-- DEMO_GIF -->` placeholder) or wherever.

Tapes provided:

- `01_new_project.tape` — `ccmux new auth-demo -d "…"` scaffolds + starts a Claude session. Matches README Tutorial 1.
- `02_dashboard.tape` — Launch ccmux, show the dashboard with multiple sessions, attach + detach. Matches Tutorial 2.
- `03_update.tape` — `ccmux update --dry-run` showing the auto-detected checkout and the steps it would run. Matches Tutorial 6.

The tapes use `Sleep` rather than `Wait` for transitions because Bubble Tea redraws can race the screenshot timing. Tweak the sleeps if your hardware is slow.
