#!/usr/bin/env bash
#
# Dirtybird Go Miner -- launcher.
#
# Your settings live in config.json next to the binary. Edit config.json directly, OR
# answer "y" below to set pool/wallet/threads interactively -- either way persists to the
# same config.json the miner reads. On Windows, run from Git Bash:  bash script.sh
set -euo pipefail
cd "$(dirname "$0")"

# --- locate the miner binary (release folder first, else build) -----------------------
if   [ -f "./go-miner.exe" ]; then BIN="./go-miner.exe"
elif [ -f "./go-miner" ];     then BIN="./go-miner"
else
    echo "go-miner not found; building (best-performance defaults)..."
    command -v go >/dev/null 2>&1 || { echo "error: install Go 1.25+ and retry." >&2; exit 1; }
    GOAMD64=v3 go build -pgo=default.pgo -trimpath -ldflags "-s -w" -o go-miner .
    BIN="./go-miner"
fi

# --- optional interactive edit (persists to config.json), then mine ------------------
read -rp "Change pool/wallet/threads? (y/N): " EDIT
case "${EDIT:-}" in [yY]*) "$BIN" --setup ;; esac

echo
echo "Starting miner (Ctrl-C to stop)..."
echo
exec "$BIN"
