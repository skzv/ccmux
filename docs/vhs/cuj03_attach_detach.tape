# CUJ C3 — Attach, work, detach: tmux is the store; ccmux is the lens.
# Render: docs/vhs/render.sh docs/vhs/cuj03_attach_detach.tape
Output docs/vhs/out/cuj03_attach_detach.gif

Set Theme "Catppuccin Mocha"
Set FontSize 16
Set Width 1280
Set Height 760
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

# The stub agent's "waiting for input" screen is now visible.
# Simulate a user reading context before typing a prompt.
Sleep 2s

# Detach via the tmux prefix (Ctrl-b) then d.
# Bubble Tea resumes once tmux exits.
Ctrl+b
Sleep 300ms
Type "d"
Sleep 1500ms

# Back in ccmux — the sessions list refreshes automatically.
Sleep 2s

# Quit.
Type "q"
Sleep 800ms
