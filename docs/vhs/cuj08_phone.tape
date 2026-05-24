# CUJ C8 — The phone workflow: open the narrow-layout dashboard, scan
# the session list, and attach to a real Claude session — the actual
# mobile UX a user does when their phone buzzes about a session needing
# input. The bell → iOS push half is handled by ccmuxd out-of-band; this
# tape covers the visible "what the user sees on their phone" portion.
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

# Let the narrow-layout dashboard paint. Devices panel shows only
# `sputnik (this)` — render.sh stubs `tailscale` so peer scan
# returns nothing (we don't want real tailnet names in a public GIF).
Sleep 2500ms
Screenshot docs/vhs/out/cuj08_phone.png

# Scroll the sessions list so the detail pane below updates per
# row, showing what each session is and which agent is running.
Type "j"
Sleep 600ms
Type "j"
Sleep 600ms
Type "k"
Sleep 800ms

# c-auth-service is highlighted — attach. Bubble Tea suspends and
# the terminal hands over to tmux/Claude. This is the part a real
# mobile user does after the push notification: tap to attach.
Enter
Sleep 3500ms

# Detach back to ccmux narrow dashboard with Ctrl-b d. On the phone
# this is `prefix then d` typed via the Moshi keyboard; here we
# send it from the tape.
Ctrl+b
Sleep 300ms
Type "d"
Sleep 2s

# Quit. Hide+clear so the GIF doesn't end on the tmux
# "[detached (from session ...)]" line printed during exit.
Type "q"
Sleep 600ms
Hide
Type "clear"
Enter
Sleep 200ms
Show
Sleep 1s
