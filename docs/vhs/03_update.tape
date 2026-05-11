# Tutorial 6: `ccmux update` end-to-end (dry-run, so it doesn't actually
# git-pull or restart your daemon during the recording).
# Render with: `vhs docs/vhs/03_update.tape`
# Output:    out/03_update.gif

Output out/03_update.gif

Set Theme "Catppuccin Mocha"
Set FontSize 16
Set Width 1100
Set Height 460
Set TypingSpeed 70ms
Set Padding 24

Type "cd ~"
Enter
Sleep 400ms

# Show help so the viewer sees the flags.
Type "ccmux update --help"
Enter
Sleep 2500ms

# Dry-run from the install location — auto-detect kicks in.
Type "ccmux update --dry-run"
Enter
Sleep 2200ms

# Hold the final frame.
Sleep 1200ms
