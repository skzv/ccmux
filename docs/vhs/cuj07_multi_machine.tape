# CUJ C7 — Sessions from anywhere: the Network screen (multi-machine overview).
# Full netsim (multiple real ccmuxd instances over loopback) is a follow-up;
# this tape shows the Network screen with a configured remote host.
# Render: docs/vhs/render.sh docs/vhs/cuj07_multi_machine.tape
Output docs/vhs/out/cuj07_multi_machine.gif

Set Theme "Catppuccin Mocha"
Set FontSize 22
Set Width 1800
Set Height 800
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
Sleep 2s

# The Home screen shows the dashboard with local sessions on the left
# and the devices panel on the right (local host + any configured remotes).
Sleep 2s

# Navigate to the Network screen (screen 7).
Type "7"
Sleep 1s

# The Network screen lists every device ccmux knows about:
# local machine + any configured remote hosts.
Sleep 2s

# Navigate down through the list.
Type "j"
Sleep 500ms
Type "j"
Sleep 500ms
Type "k"
Sleep 500ms

Sleep 1500ms

# Hold the final TUI frame so the GIF ends on the screen content,
# not on a quit + shell prompt (which can flash the tmux
# "[detached ...]" line — reads as an error on a public demo).
Sleep 1500ms
