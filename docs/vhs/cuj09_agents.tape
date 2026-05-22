# CUJ C9 — Tune Claude Code itself: the Agents screen.
# Shows the Claude / Codex / Antigravity sub-tabs and global CLAUDE.md.
# Render: docs/vhs/render.sh docs/vhs/cuj09_agents.tape
Output docs/vhs/out/cuj09_agents.gif

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

# Go to the Agents screen (key 5).
Type "5"
Sleep 1s

# We land on the Claude sub-tab by default.
# Read the current settings for a moment.
Sleep 2s

# Tab to the Codex sub-tab.
Tab
Sleep 1500ms

# Tab to the Antigravity sub-tab.
Tab
Sleep 1500ms

# Tab back to Claude (wraps around).
Tab
Sleep 1s

# Press ? to show the help overlay and reveal available actions.
Type "?"
Sleep 2s

# Dismiss help.
Type "?"
Sleep 500ms

# Quit.
Type "q"
Sleep 800ms
