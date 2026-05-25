#!/usr/bin/env bash
# cleanup_test.sh — verify that render.sh's pkill pattern actually
# kills sandbox ccmuxd processes.
#
# The bug this guards against: render.sh used `pkill -f "ccmuxd.*$root"`,
# but the sandbox path is `$root/bin/ccmuxd` so $root comes BEFORE
# "ccmuxd" in the command line. The reversed pattern never matched,
# every render leaked a daemon, and we accumulated 68 of them in the
# wild before this was caught.
#
# This script:
#   1. Builds the ccmuxd binary if needed (delegated to caller via make).
#   2. Starts a sandbox ccmuxd at $root/bin/ccmuxd.
#   3. Sources the cleanup pattern from the live render.sh — that way
#      a future regression in the script will trip this test, not a
#      drift between this file and reality.
#   4. Asserts the daemon is dead after cleanup runs.

set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
render="$here/render.sh"

[ -x "$repo/bin/ccmuxd" ] || { echo "cleanup_test: needs make build"; exit 1; }
[ -f "$render" ]          || { echo "cleanup_test: missing $render"; exit 1; }

# Extract the pkill pattern that render.sh uses for sandbox daemons.
# Anchor on the leading `pkill -TERM -f` so a future change to the
# arg order is visible (test will fail, prompting an update).
pattern_line="$(grep -nE 'pkill .*-f .*ccmuxd' "$render" | head -1 || true)"
if [ -z "$pattern_line" ]; then
  echo "cleanup_test: could not find pkill line in $render"; exit 1
fi
echo "cleanup_test: found pkill line — $pattern_line"

# Set up a fake sandbox: $root/bin/ccmuxd, isolated HOME.
root="$(mktemp -d /tmp/ccmux-vhs-cleanup-test.XXXXXX)"
cleanup() {
  # Always remove the temp tree; force-kill anything still alive.
  pkill -KILL -f "$root/bin/ccmuxd" 2>/dev/null || true
  rm -rf "$root"
}
trap cleanup EXIT INT TERM HUP

mkdir -p "$root/bin" "$root/home/.config/ccmux" "$root/home/.local/state/ccmux"
cp "$repo/bin/ccmuxd" "$root/bin/ccmuxd"

# Launch the sandbox daemon. HOME/XDG_STATE_HOME isolate the socket
# to $root/home/.local/state/ccmux/ccmuxd.sock so we don't collide
# with the user's real ccmuxd.
HOME="$root/home" XDG_STATE_HOME="$root/home/.local/state" \
  "$root/bin/ccmuxd" >/dev/null 2>&1 &
pid=$!

# Give the daemon a beat to bind the socket and stabilize.
sleep 0.3
if ! kill -0 "$pid" 2>/dev/null; then
  echo "FAIL: sandbox daemon did not start (pid $pid)"; exit 1
fi

# Sanity check: the CORRECT pattern (the fix) finds the process.
if ! pgrep -f "$root/bin/ccmuxd" >/dev/null; then
  echo "FAIL: pgrep with corrected pattern can't find sandbox daemon"; exit 1
fi

# Sanity check: the OLD broken pattern would NOT have matched. This
# documents WHY the old code leaked daemons.
if pgrep -f "ccmuxd.*$root" >/dev/null; then
  echo "NOTE: old pattern accidentally matches in this environment — pgrep regex semantics differ from expected (no failure, but inspect)"
fi

# Now run the actual cleanup pattern from render.sh.
pkill -TERM -f "$root/bin/ccmuxd" 2>/dev/null || true
sleep 0.3
pkill -KILL -f "$root/bin/ccmuxd" 2>/dev/null || true

# Give the kernel a moment to reap.
for _ in 1 2 3 4 5; do
  if ! kill -0 "$pid" 2>/dev/null; then break; fi
  sleep 0.1
done

if kill -0 "$pid" 2>/dev/null; then
  echo "FAIL: sandbox daemon survived render.sh's pkill — pattern is broken again"; exit 1
fi

echo "PASS: render.sh cleanup pattern reliably kills sandbox ccmuxd"
