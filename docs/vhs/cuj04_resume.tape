# CUJ C4 — Resume where you left off: conversation continuity across restarts.
# Render: docs/vhs/render.sh docs/vhs/cuj04_resume.tape
Output docs/vhs/out/cuj04_resume.gif

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

# Let the dashboard settle.
Sleep 3500ms

# Switch to the Conversations screen.
Type "2"
Sleep 1s

# Conversations load from ~/.claude. Navigate through the list.
Sleep 1500ms
Type "j"
Sleep 500ms
Type "j"
Sleep 500ms
Type "k"
Sleep 500ms

# Select (resume) the highlighted conversation.
# This launches `claude --resume <uuid>` in a new tmux session.
Enter
Sleep 3s

# Resuming lands us in the stub agent's session (Bubble Tea suspends).
# In a real run, Claude picks up the prior conversation thread.
Sleep 2s

# Detach back to ccmux.
Ctrl+b
Sleep 300ms
Type "d"
Sleep 1500ms

# ccmux resumes and the home screen shows the resumed session.
Sleep 1500ms

# Quit.
Type "q"
Sleep 800ms
