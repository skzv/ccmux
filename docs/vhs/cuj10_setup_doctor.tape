# CUJ C10 — First-run setup & doctor: dependency checks, then Settings screen.
# `ccmux doctor` is the CLI health check; the Settings screen (6) shows
# the live runtime status (daemon, Moshi, hosts).
# Render: docs/vhs/render.sh docs/vhs/cuj10_setup_doctor.tape
Output docs/vhs/out/cuj10_setup_doctor.gif

Set Theme "Catppuccin Mocha"
Set FontSize 22
Set Width 1800
Set Height 900
Set Padding 20
Set TypingSpeed 80ms

# Start clean in the (fake) home dir.
Hide
Type "cd ~ && clear"
Enter
Sleep 500ms
Show

# Run the doctor check first — it's the first thing a new user does.
Type "ccmux doctor"
Sleep 300ms
Enter
Sleep 3s

# Pause so the viewer can read the output.
Sleep 2s

# Clear and launch the TUI.
Type "clear"
Enter
Sleep 300ms
Type "ccmux"
Sleep 300ms
Enter
Sleep 2s

# Navigate to Settings (screen 6) — the runtime status view.
Type "6"
Sleep 1s

# Read the settings for a moment (shows daemon, Moshi, theme, paths).
Sleep 3s

# Navigate down through the settings rows. Tightened to 250ms per
# step — earlier 400ms had no visible state change between presses
# and made the demo feel sluggish.
Type "j"
Sleep 250ms
Type "j"
Sleep 250ms
Type "j"
Sleep 250ms

Sleep 1s

# Hold the final TUI frame so the GIF ends on the screen content,
# not on a quit + shell prompt (which can flash the tmux
# "[detached ...]" line — reads as an error on a public demo).
Sleep 1500ms
