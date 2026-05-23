#!/usr/bin/env bash
# brew-test.sh — smoke-test a Homebrew install of ccmux in an
# isolated sandbox, without disturbing your dev install or running
# ccmuxd.
#
# What "isolated" means here:
#   - ccmux's own state (config, db, sessions, claude transcripts)
#     comes from a fresh $HOME, so this test never mutates your real
#     ~/.config/ccmux/ or ~/.local/state/ccmux/.
#   - Brew itself still installs to the system prefix ($(brew --prefix)/bin/)
#     because brew has no notion of relocatable prefixes. The script
#     invokes the brew binaries by absolute path so PATH ordering vs.
#     your dev install (~/.local/bin/ccmux) doesn't matter.
#   - Cleanup uninstalls the formula on exit unless --keep.
#
# Modes:
#   ./scripts/brew-test.sh              install from the cloned tap's Formula/ccmux.rb (default;
#                                       works regardless of tap visibility, requires `gh` auth
#                                       if the tap is private)
#   ./scripts/brew-test.sh --tap        install from the public tap (`brew install skzv/tap/ccmux`)
#   ./scripts/brew-test.sh --keep       leave the brew install in place after the run for manual poking
#
# Requirements: brew, gh (for cloning the private tap).
set -euo pipefail

mode=local
keep=false
while [ $# -gt 0 ]; do
  case "$1" in
    --tap)  mode=tap;  shift ;;
    --keep) keep=true; shift ;;
    -h|--help) sed -n '2,28p' "$0"; exit 0 ;;
    *) echo "brew-test: unknown flag: $1" >&2; exit 2 ;;
  esac
done

command -v brew >/dev/null 2>&1 || { echo "brew-test: brew not installed" >&2; exit 1; }
[ "$mode" = local ] && ! command -v gh >/dev/null 2>&1 && { echo "brew-test: gh not installed (needed to clone the private tap)" >&2; exit 1; }

brew_prefix=$(brew --prefix)
brew_ccmux="$brew_prefix/bin/ccmux"
brew_ccmuxd="$brew_prefix/bin/ccmuxd"

# Fresh sandbox HOME so ccmux reads/writes its own scratch state.
root=$(mktemp -d "${TMPDIR:-/tmp}/ccmux-brew-test.XXXXXX")
export HOME="$root/home"
export XDG_CONFIG_HOME="$HOME/.config"
export XDG_STATE_HOME="$HOME/.local/state"
mkdir -p "$HOME" "$XDG_CONFIG_HOME" "$XDG_STATE_HOME" "$HOME/Projects"

# Don't inherit a tmux session from the parent shell — `ccmux doctor`
# would see $TMUX and try to inspect the wrong server.
unset TMUX TMUX_TMPDIR

cleanup() {
  if [ "$keep" = false ]; then
    brew uninstall --ignore-dependencies ccmux >/dev/null 2>&1 || true
  fi
  rm -rf "$root"
}
trap cleanup EXIT

case "$mode" in
  local)
    tap_dir="$root/tap"
    echo "== cloning skzv/homebrew-tap"
    gh repo clone skzv/homebrew-tap "$tap_dir" -- --depth=1 -q
    formula="$tap_dir/Formula/ccmux.rb"
    [ -f "$formula" ] || { echo "brew-test: no Formula/ccmux.rb in tap (still at repo root?)" >&2; exit 1; }
    echo "== brew install $formula"
    brew install "$formula"
    ;;
  tap)
    echo "== brew tap skzv/homebrew-tap"
    brew tap skzv/homebrew-tap 2>/dev/null || true
    echo "== brew install skzv/homebrew-tap/ccmux"
    brew install skzv/homebrew-tap/ccmux
    ;;
esac

# --- Smoke checks ----------------------------------------------------
# Invoke by absolute path so PATH ordering vs. ~/.local/bin/ccmux
# (dev install) doesn't affect which binary actually runs.

echo
echo "== brew install location:"
ls -l "$brew_ccmux" "$brew_ccmuxd"

echo
echo "== ccmux --help (first 6 lines):"
"$brew_ccmux" --help 2>&1 | head -6

echo
echo "== ccmuxd --version:"
"$brew_ccmuxd" --version 2>&1 || echo "(no --version flag — checking the binary runs at all)"

echo
echo "== ccmux doctor (sandboxed HOME=$HOME):"
# doctor exits non-zero when deps are missing — that's informational here,
# not a failure condition for the brew install itself.
"$brew_ccmux" doctor 2>&1 || true

echo
echo "== brew-test PASSED"
echo "   installed:   $brew_ccmux"
echo "   sandbox HOME: $HOME"

if [ "$keep" = true ]; then
  echo "   --keep: leaving install in place. Remove with:"
  echo "     brew uninstall --ignore-dependencies ccmux"
  echo "   Sandbox HOME preserved at $HOME (delete when done)."
  trap - EXIT
fi
