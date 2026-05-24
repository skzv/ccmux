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
Sleep 2s

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

# Submit the form with Claude — creates the dir and ATTACHES to the
# brand-new session (form-submit goes through createSession which
# attaches immediately, not just creates). Hold briefly so Claude's
# banner is visible (proves the agent really booted), then detach
# and return to Home to show the api-service session now in the
# dashboard list — that's the actual "win" of this CUJ.
Enter
Sleep 3s

# Detach back to ccmux. Without this, sending "1" would go to
# Claude (which ignores it) instead of switching ccmux screens.
Ctrl+b
Sleep 300ms
Type "d"
Sleep 1500ms

# Navigate to Home to show the populated session list with the new
# api-service entry.
Type "1"
Sleep 2500ms

# Quit ccmux. Hide+clear so the tape doesn't end on the tmux
# "[detached (from session ...)]" line ccmux prints on exit —
# that line reads as an error on a public demo.
Type "q"
Sleep 600ms
Hide
Type "clear"
Enter
Sleep 200ms
Show
Sleep 1s

