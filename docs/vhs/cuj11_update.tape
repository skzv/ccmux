# CUJ C11 — Stay current: update banner on the dashboard.
# Run with: CCMUX_UPDATE_DEMO=true docs/vhs/render.sh docs/vhs/cuj11_update.tape
# render.sh sets up a git repo that is 1 commit behind origin so the
# auto-update check fires and the dashboard shows the update banner.
# Render: CCMUX_UPDATE_DEMO=true docs/vhs/render.sh docs/vhs/cuj11_update.tape
Output docs/vhs/out/cuj11_update.gif

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

# Launch the TUI. The background update check runs on startup.
# Because the local git repo is 1 commit behind the bare remote,
# the check completes quickly and the dashboard banner appears.
Type "ccmux"
Sleep 300ms
Enter

# Wait for the update check to complete and the banner to paint.
Sleep 3s
Screenshot docs/vhs/out/cuj11_update.png

# Show the update banner for a moment, then show the user acting on it.
Sleep 1500ms

# Quit to shell to run the update command.
Type "q"
Sleep 500ms
# Use --dry-run so the demo shows the plan ccmux would execute
# without actually running `make install` against the demo's seeded
# checkout (whose Makefile has no install target). On a real install
# the same command minus --dry-run performs the upgrade + daemon
# restart end-to-end.
Type "ccmux update --dry-run"
Sleep 300ms
Enter
Sleep 3s
