#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
cat >"$tmp/manifest" <<EOF
CUSTOM_VERSION=9.9.9
CUSTOM_LOG_BASENAME=$tmp/miner
EOF
export HIVE_MANIFEST="$tmp/manifest"

fail() { echo "FAIL: $1" >&2; echo "  stats=$stats" >&2; exit 1; }

# --- a live status line, CR-overwritten and colourised the way the miner writes it
printf '\033[36m[DIRTYBIRD]\033[0m 23.76 KH/s (20.71 KH/s avg) | Height:7212998 | Miniblocks:1998 | Blocks:262 | REJ:4 | Diff:312M | 01:02:03 | funnel:12.34%%\r' >"$tmp/miner.log"
(
    . config/h-stats.sh
    [[ $stats == *'"hs": [23.76]'*   ]] || fail "hs"
    [[ $stats == *'"uptime": 3723'*  ]] || fail "uptime"
    [[ $stats == *'"ar": [1998, 4]'* ]] || fail "ar"
    [[ $stats == *'"ver": "9.9.9"'*  ]] || fail "ver from manifest"
)

# --- no log at all: the dashboard gets a live zero, never stale or malformed data
rm -f "$tmp/miner.log"
(
    . config/h-stats.sh
    [[ $khs == 0 ]]               || fail "no-log khs should be 0, got $khs"
    [[ $stats == *'"hs": [0]'*  ]] || fail "no-log hs"
    [[ $stats == *'"ar": [0, 0]'* ]] || fail "no-log ar"
)

# --- log present but no status line yet (startup, or a reconnect). This is the
# case that makes grep exit 1; without the `|| true` in h-stats.sh, sourcing it
# under `set -e` aborts here instead of reporting zeros.
printf 'connecting to pool\nresolving daemon\n' >"$tmp/miner.log"
(
    . config/h-stats.sh
    [[ $khs == 0 ]]              || fail "pre-status khs should be 0, got $khs"
    [[ $stats == *'"hs": [0]'* ]] || fail "pre-status hs"
)

echo "h-stats.sh: all cases passed"
