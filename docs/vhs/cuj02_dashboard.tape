# CUJ C2 — The dashboard: every session at a glance.
# Render: docs/vhs/render.sh docs/vhs/cuj02_dashboard.tape
Output docs/vhs/out/cuj02_dashboard.gif

Set Theme "Catppuccin Mocha"
Set FontSize 16
Set Width 1600
Set Height 1000
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

# Let the dashboard paint and the daemon's first poll land.
Sleep 4s
Screenshot docs/vhs/out/cuj02_dashboard.png
Sleep 2s

# Scroll through the sessions list to show navigation.
Type "j"
Sleep 500ms
Type "j"
Sleep 500ms
Type "k"
Sleep 500ms

Sleep 1s

# Quit.
Type "q"
Sleep 800ms
