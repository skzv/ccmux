#!/usr/bin/env bash
# traffic.sh — snapshot GitHub traffic for ccmux + the Homebrew tap
# and append one row to a CSV. Intended as a daily cron so the
# 14-day rolling traffic API window doesn't lose history.
#
# Output columns (append-only — never rearrange):
#   timestamp
#   tap_clones_total, tap_clones_uniques        (skzv/homebrew-tap)
#   tap_views_total,  tap_views_uniques
#   ccmux_clones_total, ccmux_clones_uniques    (skzv/ccmux)
#   ccmux_views_total,  ccmux_views_uniques
#   latest_release_downloads                    (sum across all assets)
#
# Default CSV: ~/.local/state/ccmux/traffic.csv. Override with --csv PATH.
#
# Requires: gh authenticated with push access to both repos.
#
# Install as a daily cron (macOS launchd):
#   cp scripts/launchd/dev.skz.ccmux.traffic.plist ~/Library/LaunchAgents/
#   launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.skz.ccmux.traffic.plist
#   launchctl kickstart gui/$(id -u)/dev.skz.ccmux.traffic   # fire once now
#
# Linux equivalent: a daily crontab entry pointing at this script,
# or a systemd timer — see the plist for the schedule (09:00 local).
set -euo pipefail

csv="${HOME}/.local/state/ccmux/traffic.csv"
while [ $# -gt 0 ]; do
  case "$1" in
    --csv) csv="$2"; shift 2 ;;
    -h|--help) sed -n '2,22p' "$0"; exit 0 ;;
    *) echo "traffic: unknown flag: $1" >&2; exit 2 ;;
  esac
done

command -v gh >/dev/null 2>&1 || { echo "traffic: gh not installed" >&2; exit 1; }

mkdir -p "$(dirname "$csv")"

header="timestamp,tap_clones_total,tap_clones_uniques,tap_views_total,tap_views_uniques,ccmux_clones_total,ccmux_clones_uniques,ccmux_views_total,ccmux_views_uniques,latest_release_downloads"
[ -f "$csv" ] || echo "$header" > "$csv"

# Each API call returns {count, uniques}; jq stitches them into "C,U".
tap_clones=$(gh   api repos/skzv/homebrew-tap/traffic/clones --jq '"\(.count),\(.uniques)"')
tap_views=$(gh    api repos/skzv/homebrew-tap/traffic/views  --jq '"\(.count),\(.uniques)"')
ccmux_clones=$(gh api repos/skzv/ccmux/traffic/clones        --jq '"\(.count),\(.uniques)"')
ccmux_views=$(gh  api repos/skzv/ccmux/traffic/views         --jq '"\(.count),\(.uniques)"')

# Sum tarball + checksum downloads on the most recent release. The
# checksums file is tiny — counting it as 1-per-install is harmless
# noise, and including it means we don't need to filter by name.
latest_tag=$(gh release list -R skzv/ccmux --limit 1 --json tagName --jq '.[0].tagName')
latest_downloads=$(gh release view "$latest_tag" -R skzv/ccmux --json assets \
  --jq '[.assets[].downloadCount] | add')

ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
row="$ts,$tap_clones,$tap_views,$ccmux_clones,$ccmux_views,$latest_downloads"

# tee so cron logs the row too; -a appends to the CSV.
echo "$row" | tee -a "$csv"
