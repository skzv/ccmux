# CUJ C5 — Pick your agent: Claude / Codex / Antigravity per project.
# Render: docs/vhs/render.sh docs/vhs/cuj05_pick_agent.tape
Output docs/vhs/out/cuj05_pick_agent.gif

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
Sleep 3500ms

# Go to Projects.
Type "3"
Sleep 600ms

# Open the new-project form.
Type "n"
Sleep 800ms

# Type a project name.
Type "data-pipeline"
Sleep 400ms

# Tab past host picker.
Tab
Sleep 300ms

# Tab to the agent picker — it starts on Claude.
Tab
Sleep 500ms

# Cycle through all three agents so the viewer can see each option.
Right
Sleep 600ms

# Now on Codex.
Right
Sleep 600ms

# Now on Antigravity.
Right
Sleep 600ms

# Back to Claude — the default for this project.
# Settle on Codex for the demo (shows the per-project choice).
Left
Sleep 500ms

# Submit with Codex selected.
Enter
Sleep 3500ms

# Return to Home to see the new Codex session in the list.
Type "1"
Sleep 2s

# Quit.
Type "q"
Sleep 800ms
