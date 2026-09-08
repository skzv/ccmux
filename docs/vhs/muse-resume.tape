# C4 Muse variant: select and resume an actual native demo conversation.
# CCMUX_MUSE_DEMO=true bash docs/vhs/render.sh docs/vhs/muse-resume.tape
Output docs/launch/v0.5.0/muse-resume.mp4
Output docs/launch/v0.5.0/muse-resume.gif
Set Theme "Catppuccin Mocha"
Set FontSize 18
Set Width 1600
Set Height 960
Set Padding 24
Set TypingSpeed 55ms
Hide
Type "cd ~ && clear"
Enter
Sleep 500ms
Show
Type "ccmux"
Enter
Sleep 2s
Type "3"
Sleep 1s
Left
Sleep 500ms
Type "/terminal"
Enter
Sleep 1s
Screenshot docs/launch/v0.5.0/muse-history.png
Sleep 1s
Enter
Sleep 3s
# Resume still asks for native workspace trust after a headless seed.
Type "1"
Enter
Sleep 1s
Type "Continue with one practical tip."
Enter
Sleep 10s
Ctrl+b
Sleep 300ms
Type "d"
Sleep 3s
