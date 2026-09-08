#!/usr/bin/env bash
# Sourced by render.sh after sandbox and cleanup initialization.
# Default echo mode is an explicitly labeled offline preview. Set
# CCMUX_MUSE_PROVIDER=meta for an authenticated launch recording.
REAL_MUSE="$(command -v muse)"
provider="${CCMUX_MUSE_PROVIDER:-echo}"
case "$provider" in echo|meta) ;; *) echo "Unsupported demo provider" >&2; exit 1;; esac
export XDG_DATA_HOME="$root/data"
export XDG_CACHE_HOME="$root/cache"
export XDG_RUNTIME_DIR="$root/runtime"
mkdir -p "$XDG_DATA_HOME" "$XDG_CACHE_HOME" "$XDG_RUNTIME_DIR"
chmod 700 "$root/home" "$XDG_DATA_HOME" "$XDG_CACHE_HOME" "$XDG_RUNTIME_DIR"
cp "$repo/bin/ccmux" "$repo/bin/ccmuxd" "$root/bin/"
# render.sh has already isolated ccmux's config. Restore only Muse's native
# config path in its wrapper; no credential files are copied or recorded.
cat > "$root/bin/muse" <<WRAP
#!/bin/sh
exec env HOME="$REAL_HOME" XDG_CONFIG_HOME="${CCMUX_MUSE_CONFIG_ROOT:-$REAL_HOME/.config}" "$REAL_MUSE" --provider "$provider" --disable-write --disable-shell "\$@"
WRAP
chmod +x "$root/bin/muse"
cat > "$root/bin/tailscale" <<'WRAP'
#!/bin/sh
exit 1
WRAP
chmod +x "$root/bin/tailscale"
printf "PROMPT='%%F{magenta}❯%%f '\nZSH_DISABLE_COMPFIX=true\n" > "$HOME/.zshrc"
touch "$HOME/.zshenv"
cat > "$XDG_CONFIG_HOME/ccmux/config.toml" <<CFG
lang = "en"
[projects]
root = "$HOME/Projects"
[tour]
shown = true
shown_version = "0.0"
[update]
auto_check = false
[daemon]
listen_tailnet = false
CFG
# A native completed conversation powers the history/resume recording.
# The raw log stays in the sandbox and is deleted by the parent's trap.
mkdir -p "$HOME/Projects/session-notes/.ccmux"
echo muse > "$HOME/Projects/session-notes/.ccmux/agent"
env HOME="$REAL_HOME" XDG_CONFIG_HOME="${CCMUX_MUSE_CONFIG_ROOT:-$REAL_HOME/.config}" "$REAL_MUSE" exec --provider "$provider" --disable-write --disable-shell --workspace "$HOME/Projects/session-notes" --disable-web-tools --no-foreign-personal-context --json 'Describe a terminal session manager in one sentence.' > "$root/seed.jsonl" 2> "$root/seed.err" || {
  cat "$root/seed.err" >&2
  exit 1
}
"$root/bin/ccmuxd" > "$root/daemon.log" 2>&1 &
sleep 2
mkdir -p "$repo/docs/launch/v0.5.0"
cd "$repo"
vhs "$tape"
