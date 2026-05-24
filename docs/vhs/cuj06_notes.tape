# CUJ C6 — Project notes, terminal-native: browse, preview, search.
# Render: docs/vhs/render.sh docs/vhs/cuj06_notes.tape
Output docs/vhs/out/cuj06_notes.gif

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
Sleep 2s

# Switch to the Notes screen.
Type "4"
Sleep 1s

# Notes loads the first project's markdown tree automatically.
# Navigate to auth-service/docs/security-model.md (5 items down):
# 0:README  1:docs/api  2:docs/architecture  3:docs/decisions  4:docs/runbook
# 5:docs/security-model — has ASCII trust-boundary diagram and attack-mitigations table.
Type "j"
Sleep 350ms
Type "j"
Sleep 350ms
Type "j"
Sleep 350ms
Type "j"
Sleep 350ms
Type "j"
Sleep 700ms

# Tab to move focus into the preview pane — Glamour renders the diagram.
Tab
Sleep 500ms

# Scroll the preview down to show the attack-mitigations table.
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms

# Tab back to the file list.
Tab
Sleep 500ms

# Open a ripgrep search across all notes in this project.
Type "/"
Sleep 500ms

# Type a search query.
Type "refresh token"
Sleep 600ms
Enter
# Bumped 1500ms → 2s so viewers can actually read the ripgrep
# matches before the j-key moves the cursor.
Sleep 2s

# Search results appear — navigate them.
Type "j"
Sleep 1s

# Preview shows the matching file (docs/api.md or architecture.md).
Sleep 2s

# Clear search and return to the full list.
Escape
Sleep 500ms

# Quit.
Type "q"
Sleep 500ms
