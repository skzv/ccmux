# CUJ C3 — Attach, work, detach: tmux is the store; ccmux is the lens.
# Render: docs/vhs/render.sh docs/vhs/cuj03_attach_detach.tape
Output docs/vhs/out/cuj03_attach_detach.gif

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

# Launch the TUI.
Type "ccmux"
Sleep 300ms
Enter

# Let the dashboard settle with its sessions list.
Sleep 2s

# Navigate down to the auth-service session.
Type "j"
Sleep 300ms

# Attach. Bubble Tea suspends; terminal hands over to the tmux session.
Enter
Sleep 2500ms

# Claude's session is now visible — a real agent, waiting for the next prompt.
Sleep 2s

# Detach: prefix-then-d returns us to ccmux. The CUJ title promised
# the round-trip; without showing the detach the demo ends inside
# tmux, which reads as "stuck" rather than "released cleanly".
Ctrl+b
Sleep 300ms
Type "d"
Sleep 2s

# Hold the final TUI frame so the GIF ends on the screen content,
# not on a `[detached ...]` line that tmux prints when ccmux exits.
Sleep 1500ms
