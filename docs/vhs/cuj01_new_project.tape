# CUJ C1 — Start a new project: directory created, agent session booted.
# Render: docs/vhs/render.sh docs/vhs/cuj01_new_project.tape
Output docs/vhs/out/cuj01_new_project.gif

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

# Change agent from Claude to Codex with the right-arrow.
Right
Sleep 400ms

# Change again to show cycling — land back on Claude.
Right
Sleep 400ms
Left
Sleep 300ms

# Submit the form — creates the directory and starts the session.
Enter
Sleep 3500ms

# Navigate back to the Home screen to see the new session in the list.
Type "1"
Sleep 2s

# Quit.
Type "q"
Sleep 800ms
