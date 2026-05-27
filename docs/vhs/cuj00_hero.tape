# CUJ hero — The full ccmux loop: 5 parallel agents, attach to real Claude,
# then tour the screens (Home → attach → Conversations → Notes →
# Agents → Settings → Home).
# Render: docs/vhs/render.sh docs/vhs/cuj00_hero.tape
Output docs/vhs/out/cuj00_hero.gif

Set Theme "Catppuccin Mocha"
Set FontSize 22
Set Width 1800
Set Height 900
Set Padding 20
Set TypingSpeed 80ms

# render.sh sleeps 4s; add a small buffer so daemon classifies all sessions.
Hide
Type "cd ~ && clear"
Enter
Sleep 2s
Show

# Launch the TUI.
Type "ccmux"
Sleep 300ms
Enter

# Dashboard settles — 5 sessions visible across 3 agents.
Sleep 2500ms

# --- Screen 1: Sessions — navigate to auth-service, attach ---
Type "j"
Sleep 500ms
Enter
Sleep 3500ms

# Detach back to ccmux.
Ctrl+b
Sleep 300ms
Type "d"
Sleep 1800ms

# --- Screen 2: Projects ---
Type "2"
Sleep 1000ms
Type "j"
Sleep 400ms
Type "j"
Sleep 400ms
Type "k"
Sleep 400ms
Type "k"
Sleep 400ms

# --- Screen 3: Conversations ---
Type "3"
Sleep 1000ms
Type "j"
Sleep 400ms
Type "j"
Sleep 350ms
Type "j"
Sleep 350ms
Type "k"
Sleep 350ms

# --- Screen 4: Notes ---
Type "4"
Sleep 1000ms
# Navigate to auth-service/docs/token-lifecycle.md (6 items down from README.md:
# 1:docs/api  2:docs/architecture  3:docs/decisions  4:docs/runbook
# 5:docs/security-model  6:docs/token-lifecycle).
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms
Type "j"
Sleep 600ms
# Switch focus to preview — Glamour renders the sequence diagram + JWT table.
Tab
Sleep 800ms
# Switch back to list, then search.
Tab
Sleep 400ms
Type "/"
Sleep 500ms
Type "refresh token"
Sleep 300ms
Enter
Sleep 800ms
Escape
Sleep 300ms

# --- Screen 5: Agents ---
Type "5"
Sleep 1000ms
Tab
Sleep 600ms
Tab
Sleep 600ms
Tab
Sleep 500ms

# --- Screen 6: Settings ---
Type "6"
Sleep 1000ms
Type "j"
Sleep 400ms
Type "j"
Sleep 400ms

# Network screen (7) is intentionally skipped — it shows live Tailscale
# peers (real machine names / IPs), which we don't want in a public GIF.

# --- Back to Screen 1: Home ---
Type "1"
Sleep 2500ms

# Hold the final TUI frame so the GIF ends on the screen content,
# not on a `[detached ...]` line that tmux prints when ccmux exits.
Sleep 1500ms
