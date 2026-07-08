#!/usr/bin/env bash
. /hive/miners/custom/go-miner/h-manifest.conf

# go-miner has no HTTP stats API; it prints the family status line to the log
# via carriage-return (\r) updates in the form (ANSI-colored):
#   [DIRTYBIRD] X.XX KH/s (Y.YY KH/s avg) | Height:N | Miniblocks:N | Blocks:N | REJ:N | Diff:NK | HH:MM:SS
# We read the tail of the log, turn the \r-overwritten line into newlines,
# strip ANSI, and scrape the freshest values for the dashboard.

LOG="${CUSTOM_LOG_BASENAME}.log"

khs=0
uptime=0
acc=0
rej=0

if [[ -f $LOG ]]; then
    line=$(tail -c 16384 "$LOG" 2>/dev/null | tr '\r' '\n' | sed 's/\x1b\[[0-9;]*[a-zA-Z]//g' | grep 'KH/s' | tail -n1)
    if [[ -n $line ]]; then
        khs=$(echo "$line" | grep -oE '[0-9]+\.[0-9]+ KH/s' | head -n1 | grep -oE '[0-9]+\.[0-9]+')
        acc=$(echo "$line" | grep -oE 'Miniblocks:[0-9]+' | grep -oE '[0-9]+')
        rej=$(echo "$line" | grep -oE 'REJ:[0-9]+' | grep -oE '[0-9]+')
        hms=$(echo "$line" | grep -oE '[0-9]{2,}:[0-9]{2}:[0-9]{2}$')
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
    "algo": "ASTROBWT"
}
END
)
