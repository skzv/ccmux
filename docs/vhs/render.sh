#!/usr/bin/env bash
# render.sh — render a ccmux demo tape to a GIF in an isolated,
# reproducible environment.
#
#   docs/vhs/render.sh docs/vhs/cuj02_dashboard.tape
#
# Isolation strategy:
#   - All tmux activity goes through a named socket ($TMUX_SOCK) so it
#     never touches the user's real server, even when run inside a tmux
#     session ($TMUX is unset before any subprocess starts).
#   - A tmux wrapper in $root/bin/ prepends -S $TMUX_SOCK so ccmuxd and
#     ccmux TUI also hit the isolated socket without any code changes.
#   - cleanup() kills only that socket; the user's sessions are untouched.
#
# Environment variables:
#   CCMUX_UPDATE_DEMO=true   — seed a git repo that's 1 commit behind
#                              so the dashboard's update banner appears.
#                              Used by cuj11_update.tape.
set -euo pipefail

tape="${1:?usage: render.sh <tape.tape>}"
repo="$(cd "$(dirname "$0")/../.." && pwd)"

command -v vhs >/dev/null 2>&1 || { echo "render: vhs not installed — 'brew install vhs'"; exit 1; }
[ -x "$repo/bin/ccmux" ] && [ -x "$repo/bin/ccmuxd" ] || { echo "render: build first — 'make build'"; exit 1; }

REAL_TMUX="$(command -v tmux)"
root="$(mktemp -d "${TMPDIR:-/tmp}/ccmux-vhs.XXXXXX")"
TMUX_SOCK="$root/tmux.sock"

export HOME="$root/home"
export XDG_CONFIG_HOME="$HOME/.config"
export XDG_STATE_HOME="$HOME/.local/state"
mkdir -p "$HOME/Projects" "$root/bin" "$HOME/.config/ccmux" \
         "$HOME/.local/state/ccmux" "$HOME/.local/bin"
export PATH="$root/bin:$HOME/.local/bin:$PATH"

# Break out of any parent tmux session so subprocesses don't inherit its socket.
unset TMUX
unset TMUX_TMPDIR

cleanup() {
  "$REAL_TMUX" -S "$TMUX_SOCK" kill-server 2>/dev/null || true
  pkill -f "ccmuxd.*$root" 2>/dev/null || true
  rm -rf "$root"
}
trap cleanup EXIT

# --- tmux wrapper: every process that invokes 'tmux' hits the isolated socket.
cat > "$root/bin/tmux" <<WRAPPER
#!/bin/sh
exec "$REAL_TMUX" -S "$TMUX_SOCK" "\$@"
WRAPPER
chmod +x "$root/bin/tmux"

# Shorthand for tmux calls within this script (real binary + socket).
T() { "$REAL_TMUX" -S "$TMUX_SOCK" "$@"; }

# --- Stub agents -----------------------------------------------------
for a in claude codex agy; do
  cat > "$root/bin/$a" <<STUB
#!/bin/sh
printf '\033[2J\033[H'
printf '\n'
printf '   %s  —  demo session\n' "$a"
printf '   \342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\n'
printf '\n'
printf '   \342\225\255\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\225\256\n'
printf '   \342\224\202  > waiting for input                  \342\224\202\n'
printf '   \342\225\260\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\225\257\n'
printf '\n'
exec sleep 86400
STUB
  chmod +x "$root/bin/$a"
done
cp "$repo/bin/ccmux" "$repo/bin/ccmuxd" "$root/bin/"

# Add ccmux to .local/bin so 'ccmux doctor' PATH check passes.
ln -sf "$root/bin/ccmux" "$HOME/.local/bin/ccmux"

# --- Shell prompt (suppress zsh new-user wizard) ---------------------
printf "PROMPT='%%F{magenta}\xe2\x9d\xaf%%f '\nZSH_DISABLE_COMPFIX=true\n" > "$HOME/.zshrc"
touch "$HOME/.zshenv"

# --- Config + fake projects ------------------------------------------
auto_check="false"
if [ "${CCMUX_UPDATE_DEMO:-}" = "true" ]; then
  auto_check="true"
fi

cat > "$HOME/.config/ccmux/config.toml" <<CFG
[projects]
root = "$HOME/Projects"

[tour]
shown = true
shown_version = "0.0"

[update]
auto_check = $auto_check
CFG

mkdir -p "$HOME/Projects/auth-service/.git" \
         "$HOME/Projects/web-dashboard/.git"
# ccmux project gets a real git repo for the update-check path.
# looksLikeCcmuxRepo checks for .git AND Makefile.
mkdir -p "$HOME/Projects/ccmux"

echo "# auth-service"  > "$HOME/Projects/auth-service/CLAUDE.md"
echo "# web-dashboard" > "$HOME/Projects/web-dashboard/CLAUDE.md"

# Pin web-dashboard to codex so a dashboard row shows the agent badge.
mkdir -p "$HOME/Projects/web-dashboard/.ccmux"
echo "codex" > "$HOME/Projects/web-dashboard/.ccmux/agent"

# --- Markdown notes (for C6 notes demo) ------------------------------
cat > "$HOME/Projects/auth-service/README.md" <<'MD'
# auth-service

JWT authentication middleware for the API gateway.

## Overview

Handles token issuance, validation, and refresh across all microservices.
Each token carries the user ID, role claims, and an expiry of 15 minutes.
Refresh tokens live 7 days and are rotated on use.

## Quick start

```bash
go run . --addr :8080 --db postgres://localhost/auth
```
MD

mkdir -p "$HOME/Projects/auth-service/docs"
cat > "$HOME/Projects/auth-service/docs/architecture.md" <<'MD'
# Architecture

## Token lifecycle

1. Client POSTs credentials to `/auth/login`
2. Server validates against the user store (bcrypt compare)
3. Issues a short-lived **JWT** (15 min) + opaque refresh token (7 d)
4. Client attaches `Authorization: Bearer <token>` to every request
5. On 401, client POSTs refresh token to `/auth/refresh`

## Key decisions

- **Stateless JWTs** — no session store; revocation via short expiry
- **Rotate-on-use refresh** — old token invalidated on each refresh cycle
- **RS256** — asymmetric signing so downstream services can verify without the private key
MD

cat > "$HOME/Projects/auth-service/docs/api.md" <<'MD'
# API Reference

## POST /auth/login

Request:
```json
{ "email": "user@example.com", "password": "..." }
```

Response `200`:
```json
{
  "token": "<jwt>",
  "refresh_token": "<opaque>",
  "expires_in": 900
}
```

## POST /auth/refresh

Request:
```json
{ "refresh_token": "<opaque>" }
```

Response `200`: same shape as `/login`.
MD

cat > "$HOME/Projects/web-dashboard/README.md" <<'MD'
# web-dashboard

React + Vite operations dashboard. Shows live session counts,
agent health, and token-quota usage pulled from the ccmux daemon.

## Stack

- **Vite** + React 18
- **Tailwind CSS** + shadcn/ui component library
- **SWR** for data fetching (polls `/api/sessions` every 5 s)
MD

# --- Claude global config (for C9 agents demo) -----------------------
mkdir -p "$HOME/.claude"
cat > "$HOME/.claude/CLAUDE.md" <<'MD'
# Global agent instructions

- Keep answers concise unless I explicitly ask for detail
- Use structured output (JSON, tables) when the answer is tabular
- Prefer TypeScript for new frontend code; Go for backend services
- Always write tests alongside implementation
MD

mkdir -p "$HOME/.claude/commands"
cat > "$HOME/.claude/commands/review.md" <<'MD'
Review the current file for correctness, test coverage, and style.
MD

cat > "$HOME/.claude/commands/explain.md" <<'MD'
Explain the selected code as if to a new team member.
MD

# --- Fake conversations (for C4 resume demo) -------------------------
# Encode project paths for ~/.claude/projects/<encoded>/<uuid>.jsonl
# Claude encodes: replace '/' with '-' in the absolute path.
auth_enc="${HOME//\//-}/Projects/auth-service"
auth_enc="${auth_enc:1}"      # strip leading char, re-prefix with -
auth_enc="-${auth_enc}"

web_enc="${HOME//\//-}/Projects/web-dashboard"
web_enc="${web_enc:1}"
web_enc="-${web_enc}"

mkdir -p "$HOME/.claude/projects/$auth_enc" \
         "$HOME/.claude/projects/$web_enc"

cat > "$HOME/.claude/projects/$auth_enc/550e8400-e29b-41d4-a716-446655440001.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Set up JWT authentication middleware with refresh-token rotation"},"timestamp":"2026-05-21T08:30:00.000Z","cwd":"$HOME/Projects/auth-service"}
{"type":"assistant","message":{"role":"assistant","content":"I'll implement the JWT middleware with RS256 signing and stateless refresh-token rotation..."},"timestamp":"2026-05-21T08:30:05.000Z"}
EOF

cat > "$HOME/.claude/projects/$web_enc/550e8400-e29b-41d4-a716-446655440002.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Add a live session-count panel to the ops dashboard"},"timestamp":"2026-05-20T14:45:00.000Z","cwd":"$HOME/Projects/web-dashboard"}
{"type":"assistant","message":{"role":"assistant","content":"I'll add a live panel using SWR to poll the /api/sessions endpoint every 5 seconds..."},"timestamp":"2026-05-20T14:45:08.000Z"}
EOF

cat > "$HOME/.claude/projects/$auth_enc/550e8400-e29b-41d4-a716-446655440003.jsonl" <<EOF
{"type":"user","message":{"role":"user","content":"Write integration tests for the /auth/refresh endpoint"},"timestamp":"2026-05-19T16:20:00.000Z","cwd":"$HOME/Projects/auth-service"}
{"type":"assistant","message":{"role":"assistant","content":"I'll write table-driven integration tests that cover the happy path, expired tokens, and rotation..."},"timestamp":"2026-05-19T16:20:10.000Z"}
EOF

# --- Git repo for update-check (C11) ---------------------------------
# Only seeds the repo; CCMUX_UPDATE_DEMO controls whether auto_check
# is on in config (set above). The repo must have .git AND Makefile
# so looksLikeCcmuxRepo returns true.
bare_remote="$root/ccmux-remote.git"
git init --bare -q "$bare_remote"

export GIT_AUTHOR_NAME="Demo"
export GIT_AUTHOR_EMAIL="demo@example.com"
export GIT_COMMITTER_NAME="Demo"
export GIT_COMMITTER_EMAIL="demo@example.com"

git -C "$HOME/Projects/ccmux" init -q
echo "# ccmux (demo)" > "$HOME/Projects/ccmux/README.md"
echo "build:" > "$HOME/Projects/ccmux/Makefile"
git -C "$HOME/Projects/ccmux" add README.md Makefile
git -C "$HOME/Projects/ccmux" commit -q -m "initial"
git -C "$HOME/Projects/ccmux" remote add origin "$bare_remote"
local_branch=$(git -C "$HOME/Projects/ccmux" rev-parse --abbrev-ref HEAD)
git -C "$HOME/Projects/ccmux" push -q -u origin "$local_branch"
# Push one extra commit to the remote, then reset local — making
# local HEAD 1 commit behind origin so the update banner fires.
git -C "$HOME/Projects/ccmux" commit -q --allow-empty -m "feat: new dashboard columns"
git -C "$HOME/Projects/ccmux" push -q origin "$local_branch"
git -C "$HOME/Projects/ccmux" reset -q --hard HEAD~1

unset GIT_AUTHOR_NAME GIT_AUTHOR_EMAIL GIT_COMMITTER_NAME GIT_COMMITTER_EMAIL

# --- Daemon + running sessions ---------------------------------------
"$root/bin/ccmuxd" >/dev/null 2>&1 &
T new-session -d -s c-auth-service  -c "$HOME/Projects/auth-service"  claude
T new-session -d -s c-web-dashboard -c "$HOME/Projects/web-dashboard" codex
T new-session -d -s c-ccmux         -c "$HOME/Projects/ccmux"         claude
# Give the daemon one full poll cycle before VHS starts recording.
sleep 3

# --- Record ----------------------------------------------------------
mkdir -p "$repo/docs/vhs/out"
cd "$repo"
vhs "$tape"
echo "render: wrote $(grep -m1 '^Output' "$tape" | awk '{print $2}')"
