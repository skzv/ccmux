# CUJ C8 — The phone workflow: narrow-layout TUI at mobile viewport size.
# The bell→iOS-push step is handled by ccmuxd out-of-band; this tape
# records the visible half — the narrow dashboard a phone-width terminal shows.
# Render: docs/vhs/render.sh docs/vhs/cuj08_phone.tape
Output docs/vhs/out/cuj08_phone.gif

Set Theme "Catppuccin Mocha"
Set FontSize 14
Set Width 430
Set Height 900
Set Padding 10
Set TypingSpeed 80ms

# Start clean in the (fake) home dir.
Hide
Type "cd ~ && clear"
Enter
Sleep 500ms
Show

# Launch the TUI at phone width.
Type "ccmux"
Sleep 300ms
Enter

# Let the narrow-layout dashboard paint.
Sleep 4s
Screenshot docs/vhs/out/cuj08_phone.png

# Scroll the sessions list.
Type "j"
Sleep 500ms
Type "j"
Sleep 500ms
Type "k"
Sleep 500ms

Sleep 1s

# Switch to Conversations on the narrow layout.
Type "2"
Sleep 1500ms

# Switch back to Home.
Type "1"
Sleep 1000ms

# Quit.
Type "q"
Sleep 800ms
