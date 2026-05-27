# CUJ C4 — Resume where you left off: conversation continuity across restarts.
# Render: docs/vhs/render.sh docs/vhs/cuj04_resume.tape
Output docs/vhs/out/cuj04_resume.gif

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

# Let the dashboard settle.
Sleep 2s

# Switch to the Conversations screen.
Type "3"
Sleep 1s

# Conversations load from ~/.claude. Navigate through the list so the
# viewer sees the detail pane update for each entry.
Sleep 1500ms
Type "j"
Sleep 800ms
Type "j"
Sleep 800ms
Type "k"
Sleep 1500ms

# NB: the actual "press Enter to resume" step is omitted intentionally.
# The tape runs in an isolated $HOME with seeded transcripts so the
# list looks lived-in, but the agent wrappers restore the real $HOME
# (for keychain/auth) — and the real Claude install has none of these
# fake UUIDs, so an actual resume would surface "no conversation
# found." Letting viewers see the populated list + detail pane gets
# the point across without that footgun.
Sleep 1s
