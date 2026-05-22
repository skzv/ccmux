# CUJ C1 — Start a new project: directory created, agent session booted.
# Render: docs/vhs/render.sh docs/vhs/cuj01_new_project.tape
Output docs/vhs/out/cuj01_new_project.gif

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
Sleep 3500ms

# Go to the Projects screen.
Type "3"
Sleep 600ms

# Open the new-project form.
Type "n"
Sleep 800ms

# Type the project name.
Type "api-service"
Sleep 400ms

# Tab past the host picker (local is the only option — it's pre-selected).
Tab
Sleep 300ms

# Tab to the agent picker.
Tab
Sleep 400ms

# Briefly show Codex is available, then return to Claude.
Right
Sleep 600ms
Left
Sleep 400ms

# Submit the form with Claude — creates the dir and boots the session.
Enter
Sleep 3500ms

# Back to the Home screen to see the new session in the list.
Type "1"
Sleep 2s

# Attach to the new api-service session to see Claude running.
Enter
Sleep 3000ms

