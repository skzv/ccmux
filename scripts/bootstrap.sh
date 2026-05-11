#!/usr/bin/env bash
#
# bootstrap.sh — make ccmux installable on a fresh machine with one
# command. Verifies the build chain (Go / git / make / Homebrew on
# macOS), offers to install whatever's missing, then hands off to
# `make setup` so the existing ccmux wizard covers tmux / mosh /
# tailscale / claude.
#
# Idempotent. Re-run any time. Never sudo's without prompting.

set -euo pipefail

# ─── pretty printing ──────────────────────────────────────────────
if [[ -t 1 ]]; then
  C_RED=$'\033[1;31m'   C_GREEN=$'\033[1;32m'  C_YELLOW=$'\033[1;33m'
  C_BLUE=$'\033[1;34m'  C_BOLD=$'\033[1m'      C_DIM=$'\033[2m'      C_OFF=$'\033[0m'
else
  C_RED=  C_GREEN=  C_YELLOW=  C_BLUE=  C_BOLD=  C_DIM=  C_OFF=
fi

step() { printf "\n${C_BLUE}▸${C_OFF} ${C_BOLD}%s${C_OFF}\n" "$1"; }
ok()   { printf "  ${C_GREEN}✓${C_OFF} %s\n" "$1"; }
warn() { printf "  ${C_YELLOW}!${C_OFF} %s\n" "$1"; }
err()  { printf "  ${C_RED}✗${C_OFF} %s\n" "$1" >&2; }
dim()  { printf "  ${C_DIM}%s${C_OFF}\n" "$1"; }

# ─── prompts ──────────────────────────────────────────────────────
# confirm "question" → returns 0 on yes, 1 on no. Default yes; pass
# "n" as the second arg to flip the default. Reads from stdin (so
# piped input works for testing); falls back to the default on EOF
# or empty input.
confirm() {
  local prompt="$1" def="${2:-y}" hint="[Y/n]" reply
  [[ "$def" == "n" ]] && hint="[y/N]"
  printf "  ${C_BOLD}?${C_OFF} %s %s " "$prompt" "$hint"
  if ! read -r reply; then
    reply="$def"
    echo "$reply"
  fi
  reply="${reply:-$def}"
  [[ "$reply" =~ ^[Yy] ]]
}

# ─── platform detection ───────────────────────────────────────────
case "$(uname -s)" in
  Darwin) OS=darwin ;;
  Linux)  OS=linux  ;;
  *)      OS=other  ;;
esac

# ─── main ─────────────────────────────────────────────────────────
printf "${C_BOLD}ccmux bootstrap${C_OFF}\n"
dim   "checks the build chain, installs what's missing, then runs make setup."
echo

# Determine the package manager command we'll suggest to the user.
PKG_INSTALL=""
case "$OS" in
  darwin)
    # On macOS we standardize on Homebrew.
    if ! command -v brew >/dev/null 2>&1; then
      step "Homebrew"
      warn "brew not found"
      echo "    The standard macOS installer for the rest of these deps. Install URL:"
      echo "      https://brew.sh/"
      if confirm "Run the official Homebrew install script now?"; then
        # The canonical one-liner; non-interactive because we already
        # asked. NONINTERACTIVE=1 keeps brew from prompting on its own.
        NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
      else
        err "brew is required on macOS — bailing. Re-run when ready."
        exit 1
      fi
    fi
    ok "Homebrew ($(brew --version | head -1))"
    PKG_INSTALL="brew install"
    ;;
  linux)
    if   command -v apt-get >/dev/null 2>&1; then PKG_INSTALL="sudo apt-get install -y"
    elif command -v dnf     >/dev/null 2>&1; then PKG_INSTALL="sudo dnf install -y"
    elif command -v pacman  >/dev/null 2>&1; then PKG_INSTALL="sudo pacman -S --noconfirm"
    elif command -v zypper  >/dev/null 2>&1; then PKG_INSTALL="sudo zypper install -y"
    elif command -v apk     >/dev/null 2>&1; then PKG_INSTALL="sudo apk add"
    else
      warn "no recognized package manager (apt / dnf / pacman / zypper / apk)"
      dim  "you'll need to install go / git / make manually before this script can proceed."
    fi
    ;;
  *)
    warn "unrecognized OS — bootstrap can only check for tools, not install them."
    ;;
esac

# ─── dep checks ───────────────────────────────────────────────────
# For each: command name on PATH, brew formula (mac), apt package (linux).
declare -a MISSING_DEPS=()

check_dep() {
  local cmd="$1" brewName="$2" aptName="$3" description="$4"
  if command -v "$cmd" >/dev/null 2>&1; then
    ok "$cmd  ($(command -v "$cmd"))"
    return 0
  fi
  warn "$cmd missing — $description"
  case "$OS" in
    darwin) MISSING_DEPS+=("$brewName") ;;
    linux)  MISSING_DEPS+=("$aptName") ;;
    *)      MISSING_DEPS+=("$cmd") ;;
  esac
}

step "Build prerequisites"
check_dep git  git  git  "needed to clone + pull updates"
check_dep make make make "drives the build (you already have it if you got this far via 'make bootstrap')"
check_dep go   go   golang "Go 1.22+ — ccmux is written in Go"

if (( ${#MISSING_DEPS[@]} > 0 )); then
  echo
  step "Install missing build deps"
  if [[ -n "$PKG_INSTALL" ]]; then
    cmd="$PKG_INSTALL ${MISSING_DEPS[*]}"
    echo "    Run: ${C_BOLD}$cmd${C_OFF}"
    echo
    if confirm "Run it now?"; then
      # shellcheck disable=SC2086
      $PKG_INSTALL ${MISSING_DEPS[@]}
    else
      err "Re-run this script once those are installed."
      exit 1
    fi
  else
    err "Install these by hand, then re-run: ${MISSING_DEPS[*]}"
    exit 1
  fi
fi

# Re-verify Go specifically — version matters for ccmux.
GO_MIN_VERSION="1.22"
if command -v go >/dev/null 2>&1; then
  GO_VERSION="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
  if [[ -n "$GO_VERSION" ]]; then
    # Lexicographic compare is wrong for "1.10" vs "1.9" but Go's
    # version scheme is consistently 1.NN so it's fine in practice.
    if [[ "$GO_VERSION" < "$GO_MIN_VERSION" ]]; then
      warn "go $GO_VERSION installed; ccmux wants ≥ $GO_MIN_VERSION"
      dim  "upgrade via your package manager and re-run."
      exit 1
    fi
    ok "go $GO_VERSION (>= $GO_MIN_VERSION)"
  fi
fi

# ─── hand off to make setup ───────────────────────────────────────
echo
step "Build + install + run ccmux setup wizard"
dim  "make setup compiles ccmux, copies it to ~/.local/bin, then runs the"
dim  "interactive wizard that handles tmux / mosh / tailscale / claude /"
dim  "gh / moshi-hook."
echo

cd "$(dirname "$0")/.."
exec make setup
