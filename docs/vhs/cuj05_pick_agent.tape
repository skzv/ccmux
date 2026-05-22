# CUJ C5 — Pick your agent: Claude / Codex / Antigravity per project.
# Render: docs/vhs/render.sh docs/vhs/cuj05_pick_agent.tape
Output docs/vhs/out/cuj05_pick_agent.gif

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
Sleep 3500ms

# Go to Projects.
Type "3"
Sleep 600ms

# Open the new-project form.
Type "n"
Sleep 800ms

# Type a project name (unique — not one of the pre-seeded fixtures).
Type "inference-api"
Sleep 400ms

# Tab past host picker.
Tab
Sleep 300ms

# Tab to the agent picker — it starts on Claude.
Tab
Sleep 500ms

# Cycle: briefly pause on Codex, then land on Antigravity (agy).
Right
Sleep 600ms

# Now on Codex — keep moving.
Right
Sleep 800ms

# Now on Antigravity — this is the agent for this project.
Enter
Sleep 3500ms

# Return to Home to see the new agy session in the list.
Type "1"
Sleep 2s

# Navigate to inference-api (3rd alphabetically: auth-service, ccmux, inference-api).
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms

# Attach to the new inference-api session — agy is running.
Enter
Sleep 3s
# "Accessing workspace" appears first, then the trust prompt — wait for it.
Enter
Sleep 2s
# Second pass in case trust prompt needed extra time to appear.
Enter
Sleep 3s
