# CUJ C5 — Pick your agent: Claude / Codex / Antigravity / Cursor per project.
# Render: docs/vhs/render.sh docs/vhs/cuj05_pick_agent.tape
Output docs/vhs/out/cuj05_pick_agent.gif

Set Theme "Catppuccin Mocha"
Set FontSize 22
Set Width 1800
Set Height 900
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
Sleep 2s

# Go to Projects.
Type "2"
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
# Hold on the trust prompt so the viewer sees Antigravity's
# "Accessing workspace" / agent header, then quit before pressing
# Enter again. Earlier passes pressed Enter twice more to dismiss
# the trust prompt, but that immediately spawned Antigravity's
# vim-based prompt editor (an empty `/var/folders/.../jetski-prompt-
# *.txt`), which made the demo end on a sea of `~` characters.
Enter
Sleep 3s

# Detach back to ccmux. Without this, sending "1" below would go
# to Antigravity (which opens its vim prompt editor on Enter) and
# the demo would end on a sea of empty-line tildes.
Ctrl+b
Sleep 300ms
Type "d"
Sleep 1500ms

# Navigate to Home — the Sessions list shows the new c-inference-api
# session with the agy badge, proving the picker assigned the right
# agent for this project.
Type "1"
Sleep 2500ms

# Hold the final TUI frame so the GIF ends on the screen content,
# not on a `[detached ...]` line that tmux prints when ccmux exits.
Sleep 1500ms
