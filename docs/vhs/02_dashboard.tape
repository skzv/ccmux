# Tutorial 2: the dashboard juggling multiple Claude sessions.
# Render with: `vhs docs/vhs/02_dashboard.tape`
# Output:    out/02_dashboard.gif
#
# Assumes you already have at least 2 sessions running so the dashboard
# looks alive. If not, run a `ccmux new` or two beforehand.

Output out/02_dashboard.gif

Set Theme "Catppuccin Mocha"
Set FontSize 16
Set Width 1200
Set Height 700
Set TypingSpeed 80ms
Set Padding 24

# Launch the TUI.
Type "ccmux"
Enter
Sleep 1800ms

# Move to the Sessions screen.
Type "s"
Sleep 1500ms

# Scroll through the list so the viewer sees state colors changing.
Type "j"
Sleep 400ms
Type "j"
Sleep 400ms
Type "j"
Sleep 600ms
Type "k"
Sleep 400ms
Type "k"
Sleep 800ms

# Open the contextual help overlay.
Type "?"
Sleep 2000ms
Escape
Sleep 600ms

# Back to dashboard.
Type "d"
Sleep 1500ms

# Quit cleanly.
Type "q"
Sleep 600ms
