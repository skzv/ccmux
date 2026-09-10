# C1/C3 Muse variant. Offline preview unless CCMUX_MUSE_PROVIDER=meta.
# CCMUX_MUSE_DEMO=true bash docs/vhs/render.sh docs/vhs/muse-start.tape
Output docs/launch/v0.5.0/muse-start.mp4
Output docs/launch/v0.5.0/muse-start.gif
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
Type "ccmux new muse-demo --agent muse"
Enter
Sleep 2s
Type "1"
Enter
Sleep 1s
Type "In one sentence: what keeps a tmux session alive?"
Enter
Sleep 10s
Screenshot docs/launch/v0.5.0/muse-session.png
Sleep 1s
Ctrl+b
Sleep 300ms
Type "d"
Sleep 800ms
Type "ccmux"
Enter
Sleep 4s
Screenshot docs/launch/v0.5.0/muse-dashboard.png
Sleep 3s
