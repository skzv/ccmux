# CUJ C3 — Attach, work, detach: tmux is the store; ccmux is the lens.
# Render: docs/vhs/render.sh docs/vhs/cuj03_attach_detach.tape
Output docs/vhs/out/cuj03_attach_detach.gif

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

# Let the dashboard settle with its sessions list.
Sleep 3500ms

# Navigate down to the auth-service session.
Type "j"
Sleep 300ms

# Attach. Bubble Tea suspends; terminal hands over to the tmux session.
Enter
Sleep 2500ms

# Claude's session is now visible — a real agent, waiting for the next prompt.
Sleep 2s
