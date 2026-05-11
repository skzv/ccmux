# Tutorial 1: scaffold a new project with one command.
# Render with: `vhs docs/vhs/01_new_project.tape`
# Output:    out/01_new_project.gif

Output out/01_new_project.gif

# Visual tuning.
Set Theme "Catppuccin Mocha"
Set FontSize 16
Set Width 1100
Set Height 540
Set TypingSpeed 60ms
Set Padding 24
Set PlaybackSpeed 1.0

# Start in the user's home dir to make the prompt readable.
Type "cd ~"
Enter
Sleep 600ms

# The headline command.
Type "ccmux new auth-demo -d "
Type `"`
Type "rebuild the login flow with passkeys"
Type `"`
Sleep 400ms
Enter

# Give scaffold + git init + Claude boot time. In real life this is
# 3-4 seconds; we don't want a 30-second GIF.
Sleep 4500ms

# Show the result with `ccmux list`.
Ctrl+C
Sleep 200ms
Type "ccmux list"
Enter
Sleep 2500ms

# Hold the final frame so the viewer can read it.
Sleep 1500ms
