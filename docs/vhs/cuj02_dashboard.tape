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
Sleep 2500ms
Screenshot docs/vhs/out/cuj02_dashboard.png

# Scroll through the sessions list to show navigation. Each j/k pause
# at 600ms gives the right pane time to refresh with the newly-
# selected session's detail block.
Type "j"
Sleep 600ms
Type "j"
Sleep 600ms
Type "j"
Sleep 600ms
Type "k"
Sleep 600ms
Type "k"
Sleep 800ms

# Quit.
Type "q"
Sleep 500ms
