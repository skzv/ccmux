# CUJ C6 — Project notes, terminal-native: browse, preview, search.
# Render: docs/vhs/render.sh docs/vhs/cuj06_notes.tape
Output docs/vhs/out/cuj06_notes.gif

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

# Switch to the Notes screen.
Type "4"
Sleep 1s

# Notes loads the first project's markdown tree automatically.
# Navigate down to read docs/architecture.md.
Type "j"
Sleep 400ms
Type "j"
Sleep 400ms

# Tab to move focus into the preview pane.
Tab
Sleep 500ms

# Scroll the preview down to show more content.
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
Sleep 300ms
Enter
Sleep 1500ms

# Search results appear — navigate them.
Type "j"
Sleep 500ms

# Preview shows the matching file (docs/api.md or architecture.md).
Sleep 1500ms

# Clear search and return to the full list.
Escape
Sleep 500ms

# Quit.
Type "q"
Sleep 800ms
