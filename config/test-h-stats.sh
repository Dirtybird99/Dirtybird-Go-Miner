#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
cat >"$tmp/manifest" <<EOF
CUSTOM_LOG_BASENAME=$tmp/miner
EOF
printf '\033[36m[DIRTYBIRD]\033[0m 23.76 KH/s (20.71 KH/s avg) | Height:7212998 | Miniblocks:1998 | Blocks:262 | REJ:4 | Diff:312M | 01:02:03 | funnel:12.34%%\r' >"$tmp/miner.log"

export HIVE_MANIFEST="$tmp/manifest"
. config/h-stats.sh

[[ $stats == *'"hs": [23.76]'* ]]
[[ $stats == *'"uptime": 3723'* ]]
[[ $stats == *'"ar": [1998, 4]'* ]]
