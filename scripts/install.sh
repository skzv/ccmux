#!/bin/sh
# install.sh — download the latest ccmux release and install ccmux +
# ccmuxd into ~/.local/bin. macOS and Linux, amd64 and arm64.
#
#   curl -fsSL https://raw.githubusercontent.com/skzv/ccmux/main/scripts/install.sh | sh
#
# This is the no-Homebrew path. If you have Homebrew, prefer:
#   brew install skzv/tap/ccmux
#
# After install, run `ccmux setup` to wire tmux / mosh / tailscale /
# agents and the ccmuxd background service.
#
# Override the install dir with CCMUX_BIN_DIR=/somewhere.
set -eu

REPO="skzv/ccmux"
BIN_DIR="${CCMUX_BIN_DIR:-$HOME/.local/bin}"

fail() { echo "install: $1" >&2; exit 1; }

os=$(uname -s)
case "$os" in
	Darwin) os=darwin ;;
	Linux)  os=linux ;;
	*) fail "unsupported OS: $os (macOS and Linux only)" ;;
esac

arch=$(uname -m)
case "$arch" in
	x86_64 | amd64)  arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) fail "unsupported architecture: $arch" ;;
esac

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar  >/dev/null 2>&1 || fail "tar is required"

archive="ccmux_${os}_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/latest/download/${archive}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "ccmux: downloading ${archive} ..."
if ! curl -fsSL "$url" -o "$tmp/$archive"; then
	echo "install: download failed — $url" >&2
	fail "no published release yet? see https://github.com/${REPO}/releases"
fi

tar -xzf "$tmp/$archive" -C "$tmp"

mkdir -p "$BIN_DIR"
for bin in ccmux ccmuxd; do
	[ -f "$tmp/$bin" ] || fail "release archive is missing $bin"
	install -m 0755 "$tmp/$bin" "$BIN_DIR/$bin"
	# macOS quarantines anything downloaded; strip it so Gatekeeper
	# doesn't silently kill the unsigned binary (notarization is
	# pending an Apple Developer account).
	if [ "$os" = darwin ]; then
		xattr -d com.apple.quarantine "$BIN_DIR/$bin" 2>/dev/null || true
	fi
done

echo "ccmux: installed ccmux + ccmuxd to ${BIN_DIR}"
case ":$PATH:" in
	*":$BIN_DIR:"*) ;;
	*) echo "ccmux: add ${BIN_DIR} to your PATH, then re-open your shell" ;;
esac
echo "ccmux: next — run 'ccmux setup'"
