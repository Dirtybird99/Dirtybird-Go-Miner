#!/usr/bin/env bash
# Fall back to the manifest beside this script rather than a fixed
# /hive/miners/custom/<name> path: mmpOS installs elsewhere, and the absolute
# form breaks silently if CUSTOM_NAME or the install root ever moves.
# BASH_SOURCE, not $0 -- the agent sources this file. HIVE_MANIFEST stays as the
# override hook test-h-stats.sh uses.
. "${HIVE_MANIFEST:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/h-manifest.conf}"

# go-miner has no HTTP stats API; Hive forces the family status line into the
# log as a plain newline record. The parser also tolerates older CR/ANSI logs:
#   [DIRTYBIRD] X.XX KH/s (Y.YY KH/s avg) | Height:N | Miniblocks:N | Blocks:N | REJ:N | Diff:NK | HH:MM:SS
# We read the tail of the log, turn the \r-overwritten line into newlines,
# strip ANSI, and scrape the freshest values for the dashboard.

LOG="${CUSTOM_LOG_BASENAME}.log"

khs=0
uptime=0
acc=0
rej=0

if [[ -f $LOG ]]; then
    # `|| true`: a log with no status line yet (startup, or a reconnect) makes
    # grep exit 1, which would abort the whole hook if the agent ever sources it
    # under `set -e`. Nothing has gone wrong -- the miner just has not printed.
    line=$(tail -c 16384 "$LOG" 2>/dev/null | tr '\r' '\n' | sed 's/\x1b\[[0-9;]*[a-zA-Z]//g' | grep 'KH/s' | tail -n1 || true)
    if [[ -n $line ]]; then
        khs=$(echo "$line" | grep -oE '[0-9]+\.[0-9]+ KH/s' | head -n1 | grep -oE '[0-9]+\.[0-9]+')
        acc=$(echo "$line" | grep -oE 'Miniblocks:[0-9]+' | grep -oE '[0-9]+')
        rej=$(echo "$line" | grep -oE 'REJ:[0-9]+' | grep -oE '[0-9]+')
        hms=$(echo "$line" | grep -oE '[0-9]{2,}:[0-9]{2}:[0-9]{2}' | tail -n1)
        if [[ -n $hms ]]; then
            h=${hms%%:*}; rest=${hms#*:}; m=${rest%%:*}; s=${rest#*:}
            uptime=$(( 10#$h * 3600 + 10#$m * 60 + 10#$s ))
        fi
    fi
fi

[[ -z $khs ]] && khs=0
[[ -z $acc ]] && acc=0
[[ -z $rej ]] && rej=0
[[ -z $uptime ]] && uptime=0

stats=$(cat <<-END
{
    "hs": [$khs],
    "hs_units": "khs",
    "uptime": $uptime,
    "ar": [$acc, $rej],
    "algo": "ASTROBWT",
    "ver": "${CUSTOM_VERSION:-dev}"
}
END
)
