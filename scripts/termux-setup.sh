#!/usr/bin/env bash
# Dirtybird Go Miner — Termux (Android) setup, ported from the sibling
# C miner's scripts/termux-setup.sh with the Rust sibling's hardening
# (checksum verification, interrupt-safe traps, termux-api timeouts,
# re-prompting wallet validation).
#
# One-liner:
#   curl -fsSL https://raw.githubusercontent.com/Dirtybird99/Dirtybird-Go-Miner/main/scripts/termux-setup.sh | bash
#
# Installs the latest release into ~/dirtybird-go-miner, prompts for
# pool/wallet/threads on first run (config.json lives beside the binary,
# which is where the miner reads it), acquires a wake-lock, and runs the
# miner under an auto-restart loop.
set -euo pipefail

REPO="Dirtybird99/Dirtybird-Go-Miner"
DEFAULT_WALLET="dero1qyvuemd6z0uzsx5ufc99f0jhyzvvpysmrd2t3526ht7a9dfh7jve2qqt0vu5y"
INSTALL_DIR="$HOME/dirtybird-go-miner"
BINARY_NAME="go-miner"
VERSION_FILE=".installed_version"
ARCHIVE_PREFIX="Dirtybird-Go-Miner-arm64-"
ARCHIVE_SUFFIX=".tar.gz"

# Daemon/pool menu (the family list).
POOL_NAMES=("Community Pools" "Rabid Mining" "dero-node.net" "DERO Foundation" "Custom address")
POOL_ADDRS=("community-pools.mysrv.cloud:10300" "dero.rabidmining.com:10300" "dero-node.net:10100" "node.derofoundation.org:10100" "")
POOL_KINDS=(
  "pool -- rewards every few seconds, best for phones"
  "pool -- rewards every few seconds, best for phones"
  "solo node -- a phone may wait hours between rewards"
  "solo node -- full blocks only, 9x the work per reward"
  ""
)

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'
info() { printf "${GREEN}[*]${NC} %s\n" "$1"; }
warn() { printf "${YELLOW}[!]${NC} %s\n" "$1"; }
err()  { printf "${RED}[x]${NC} %s\n" "$1" >&2; }
note() { printf "${CYAN}[i]${NC} %s\n" "$1"; }

usage() {
  cat <<EOF
Usage: termux-setup.sh [--update] [--reconfigure] [--uninstall] [--help]
  --update       force re-download of the latest release
  --reconfigure  re-prompt for pool/wallet/threads
  --uninstall    remove $INSTALL_DIR
EOF
}

DO_UPDATE=0
DO_RECONFIG=0
for arg in "$@"; do
  case "$arg" in
    --update) DO_UPDATE=1 ;;
    --reconfigure) DO_RECONFIG=1 ;;
    --uninstall)
      rm -rf "$INSTALL_DIR"
      info "Removed $INSTALL_DIR."
      exit 0
      ;;
    -h|--help) usage; exit 0 ;;
    *) err "Unknown option: $arg"; usage; exit 2 ;;
  esac
done

# Under `curl | bash` prompts read /dev/tty; probe it once so a session with
# no terminal (Termux:Boot, widget, ssh without a pty) refuses loudly instead
# of silently configuring the project's default wallet.
INTERACTIVE=1
if ! { : </dev/tty; } 2>/dev/null; then
  INTERACTIVE=0
fi

# ---- platform gate -------------------------------------------------------
if [ "$(uname -o 2>/dev/null)" != "Android" ]; then
  err "This script is for Termux on Android."
  note "Other platforms: https://github.com/$REPO/releases"
  exit 1
fi
if [ "$(uname -m)" != "aarch64" ]; then
  err "Unsupported CPU architecture: $(uname -m) (need aarch64)."
  exit 1
fi

# ---- dependencies --------------------------------------------------------
NEED_PKGS=()
command -v tar >/dev/null 2>&1 || NEED_PKGS+=(tar)
command -v jq >/dev/null 2>&1 || NEED_PKGS+=(jq)
if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
  NEED_PKGS+=(curl)
fi
if [ "${#NEED_PKGS[@]}" -gt 0 ]; then
  info "Installing packages: ${NEED_PKGS[*]}"
  # </dev/null: under curl|bash, stdin is the script body and an apt prompt
  # would eat it. Output goes to a log echoed on failure — a silent hang and
  # a mirror outage must be distinguishable.
  PKG_LOG="$(mktemp)"
  if ! { pkg update -y </dev/null >"$PKG_LOG" 2>&1 && pkg install -y "${NEED_PKGS[@]}" </dev/null >>"$PKG_LOG" 2>&1; }; then
    cat "$PKG_LOG" >&2
    rm -f "$PKG_LOG"
    err "Package install failed (log above). Run manually: pkg install -y ${NEED_PKGS[*]}"
    exit 1
  fi
  rm -f "$PKG_LOG"
fi

fetch() { # url -> stdout
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --connect-timeout 5 --max-time 10 "$1"
  else
    wget -qO- --tries=2 -T 10 "$1"
  fi
}
fetch_to() { # url file (longer budget: release binaries)
  if command -v curl >/dev/null 2>&1; then
    curl -fSL --connect-timeout 5 --max-time 300 -o "$2" "$1"
  else
    wget --tries=2 -T 300 -O "$2" "$1"
  fi
}
# The plain-web redirect is not rate-limited; api.github.com allows 60
# unauthenticated requests per hour PER IP, and phones behind carrier CGNAT
# share that budget with thousands of neighbors.
latest_tag_from_redirect() {
  local loc
  if command -v curl >/dev/null 2>&1; then
    loc="$(curl -fsSI --connect-timeout 5 --max-time 10 "https://github.com/$REPO/releases/latest" 2>/dev/null | awk 'tolower($1)=="location:"{print $2}' | tr -d '\r')"
  else
    loc="$(wget -q --max-redirect=0 -S -T 10 -O /dev/null "https://github.com/$REPO/releases/latest" 2>&1 | awk 'tolower($1)=="location:"{print $2}' | tr -d '\r')"
  fi
  printf '%s' "${loc##*/tag/}"
}
latest_release_tag() {
  local tag
  tag="$(fetch "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | jq -r '.tag_name // empty' || true)"
  if [ -z "$tag" ]; then
    tag="$(latest_tag_from_redirect || true)"
  fi
  printf '%s' "$tag"
}

# ---- install / update ----------------------------------------------------
mkdir -p "$INSTALL_DIR"
cd "$INSTALL_DIR"

LATEST_TAG=""
if [ "$DO_UPDATE" -eq 0 ] && [ -f "$VERSION_FILE" ] && [ -x "./$BINARY_NAME" ]; then
  INSTALLED_TAG="$(cat "$VERSION_FILE")"
  info "Using installed release marker: $INSTALLED_TAG."
  info "Checking for updates..."
  LATEST_TAG="$(latest_release_tag || true)"
else
  info "Fetching latest release info..."
  LATEST_TAG="$(latest_release_tag || true)"
  if [ -z "$LATEST_TAG" ]; then
    err "Could not determine latest release (network problem, or GitHub API rate limit on this IP)."
    exit 1
  fi
  if ! printf '%s' "$LATEST_TAG" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
    err "Unexpected release tag format: $LATEST_TAG"
    exit 1
  fi
  ARCHIVE="${ARCHIVE_PREFIX}${LATEST_TAG}${ARCHIVE_SUFFIX}"
  URL="https://github.com/$REPO/releases/download/$LATEST_TAG/$ARCHIVE"
  info "Downloading $ARCHIVE..."
  fetch_to "$URL" "$ARCHIVE"

  # Integrity: the release publishes SHA256SUMS.txt; a binary that will run
  # unattended for days gets verified before it ever executes.
  info "Verifying checksum..."
  fetch "https://github.com/$REPO/releases/download/$LATEST_TAG/SHA256SUMS.txt" > SHA256SUMS.txt.tmp
  EXPECTED_LINE="$(awk -v f="$ARCHIVE" '$2 == f' SHA256SUMS.txt.tmp)"
  rm -f SHA256SUMS.txt.tmp
  if [ -z "$EXPECTED_LINE" ]; then
    rm -f "$ARCHIVE"
    err "SHA256SUMS.txt has no entry for $ARCHIVE."
    exit 1
  fi
  if ! printf '%s\n' "$EXPECTED_LINE" | sha256sum -c - >/dev/null 2>&1; then
    rm -f "$ARCHIVE"
    err "Checksum mismatch for $ARCHIVE — corrupt or tampered download; not installing."
    exit 1
  fi

  # Never let a fresh marker sit next to a stale binary.
  rm -f "./$BINARY_NAME"
  find . -maxdepth 1 -type d -name "${ARCHIVE_PREFIX}*" -exec rm -rf {} +
  tar -xzf "$ARCHIVE"
  rm -f "$ARCHIVE"
  PKG_DIR="${ARCHIVE%"$ARCHIVE_SUFFIX"}"
  if [ ! -f "$PKG_DIR/$BINARY_NAME" ]; then
    err "Archive did not contain $PKG_DIR/$BINARY_NAME."
    exit 1
  fi
  mv "$PKG_DIR/$BINARY_NAME" "./$BINARY_NAME"
  rm -rf "$PKG_DIR"
  chmod +x "./$BINARY_NAME"
  printf '%s\n' "$LATEST_TAG" > "$VERSION_FILE"
  info "Installed $LATEST_TAG."
fi

# ---- version verification (surface stale installs) ------------------------
BINARY_VERSION="$("./$BINARY_NAME" --version 2>/dev/null || true)"
if [ -z "$BINARY_VERSION" ]; then
  err "Installed miner could not report its version."
  err "Run this script with --update to repair the installation."
  exit 1
fi
if [ -n "$LATEST_TAG" ] && [ "$BINARY_VERSION" != "$BINARY_NAME $LATEST_TAG" ]; then
  warn "Update available: $BINARY_VERSION -> $BINARY_NAME $LATEST_TAG."
  warn "Update before benchmarking:"
  note "curl -fsSL https://raw.githubusercontent.com/$REPO/main/scripts/termux-setup.sh | bash -s -- --update"
fi

# ---- configuration --------------------------------------------------------
if [ "$DO_RECONFIG" -eq 1 ] || [ ! -f config.json ]; then
  if [ "$INTERACTIVE" -eq 0 ]; then
    err "No terminal available for the pool/wallet/threads prompts."
    note "Run this script once from an interactive Termux session, or write"
    note "$INSTALL_DIR/config.json yourself (keys: daemon-address, wallet, threads)."
    exit 1
  fi
  printf "${CYAN}Select a daemon/pool:${NC}\n"
  for i in 0 1 2 3 4; do
    printf "  [%d] %-16s %s\n" "$((i + 1))" "${POOL_NAMES[$i]}" "${POOL_ADDRS[$i]}"
    if [ -n "${POOL_KINDS[$i]}" ]; then
      printf "      %-16s ${YELLOW}%s${NC}\n" "" "${POOL_KINDS[$i]}"
    fi
  done
  POOL=""
  while [ -z "$POOL" ]; do
    read -rp "  Choice [1]: " CHOICE </dev/tty || CHOICE=""
    CHOICE="${CHOICE:-1}"
    case "$CHOICE" in
      [1-4]) POOL="${POOL_ADDRS[$((CHOICE - 1))]}" ;;
      5)
        printf "${CYAN}Daemon/pool address${NC} (host:port, no scheme prefix)\n"
        read -rp "  Address: " POOL </dev/tty || POOL=""
        if ! printf '%s' "$POOL" | grep -qE '^[A-Za-z0-9._-]+:[0-9]{1,5}$'; then
          warn "Expected host:port (e.g. dero-node.net:10100)."
          POOL=""
        fi
        ;;
      *) warn "Enter a number from 1 to 5." ;;
    esac
  done
  info "Using: $POOL"

  # Re-prompt on a bad wallet: a fat-fingered paste is the most likely input
  # error on a phone, and the download already succeeded. The length floor
  # rejects clipboard-truncated pastes that would mine to a dead address.
  printf "${CYAN}DERO wallet address${NC}\n"
  printf "  Press Enter to use the PROJECT wallet: %s\n" "$DEFAULT_WALLET"
  WALLET=""
  while [ -z "$WALLET" ]; do
    read -rp "  Wallet: " WALLET </dev/tty || WALLET=""
    WALLET="${WALLET:-$DEFAULT_WALLET}"
    if ! printf '%s' "$WALLET" | grep -qE '^(dero1|deto1)[a-z0-9]{60,}$'; then
      warn "Invalid wallet address (must start dero1/deto1 followed by 60+ lowercase alphanumerics)."
      WALLET=""
    fi
  done

  CORES="$(nproc 2>/dev/null || echo 4)"
  DEFAULT_THREADS=$((CORES - 1))
  [ "$DEFAULT_THREADS" -lt 1 ] && DEFAULT_THREADS=1
  printf "${CYAN}Mining threads${NC}\n"
  note "-pin/-high have no effect on Android; use the thread count to balance"
  note "hashrate, temperature, and battery use."
  while true; do
    read -rp "  Threads [${DEFAULT_THREADS}] (1-${CORES}): " INPUT_THREADS </dev/tty || INPUT_THREADS=""
    INPUT_THREADS="${INPUT_THREADS:-$DEFAULT_THREADS}"
    if printf '%s' "$INPUT_THREADS" | grep -qE '^[1-9][0-9]*$' && [ "$INPUT_THREADS" -le "$CORES" ]; then
      THREADS="$INPUT_THREADS"
      break
    fi
    warn "Enter a number from 1 to $CORES."
  done

  # Atomic write: an interrupted plain redirect leaves truncated JSON that
  # the restart loop would then hammer forever.
  cat > config.json.tmp <<EOF
{
  "daemon-address": "$POOL",
  "wallet": "$WALLET",
  "threads": $THREADS
}
EOF
  mv config.json.tmp config.json
  info "Config written to $INSTALL_DIR/config.json"
else
  info "Using existing config.json (use --reconfigure to change)."
fi

# A corrupt config makes the miner exit instantly; catch it here with a
# useful message instead of letting the restart loop discover it all night.
if ! jq empty config.json >/dev/null 2>&1; then
  err "config.json is not valid JSON."
  note "Re-run with --reconfigure to rebuild it."
  exit 1
fi

# ---- single instance -------------------------------------------------------
# The Termux wake-lock is app-global: a second session exiting would release
# it under the first session's miner (hashrate silently dies when the screen
# sleeps). One session at a time.
if command -v flock >/dev/null 2>&1; then
  exec 9>"$INSTALL_DIR/.lock"
  if ! flock -n 9; then
    err "Another Dirtybird session appears to be running (the wake-lock is shared)."
    note "Stop it first; if this is stale, remove $INSTALL_DIR/.lock and retry."
    exit 1
  fi
fi

# ---- battery advisory ------------------------------------------------------
# timeout: the termux-api shim blocks forever when the Termux:API app is not
# installed; the CLI existing proves nothing.
if command -v termux-battery-status >/dev/null 2>&1; then
  BATTERY_JSON="$(timeout 5 termux-battery-status 2>/dev/null || true)"
  if [ -n "$BATTERY_JSON" ]; then
    BAT_PCT="$(printf '%s' "$BATTERY_JSON" | jq -r '.percentage // empty')"
    BAT_PLUGGED="$(printf '%s' "$BATTERY_JSON" | jq -r '.plugged // empty')"
    if [ -n "$BAT_PCT" ] && [ "$BAT_PCT" -lt 40 ]; then
      warn "Battery is ${BAT_PCT}%. Mining drains battery fast; consider charging."
    fi
    if [ "$BAT_PLUGGED" = "UNPLUGGED" ]; then
      warn "Device is running on battery power. Mining drains battery fast."
    fi
  fi
fi

# ---- wake-lock -------------------------------------------------------------
WAKE_LOCKED=0
release_lock() {
  if [ "$WAKE_LOCKED" -eq 1 ]; then
    WAKE_LOCKED=0
    termux-wake-unlock >/dev/null 2>&1 || true
    info "Wake-lock released."
  fi
}
# INT/TERM/HUP must EXIT after releasing: a returning trap resumes the script,
# and Ctrl-C landing in the backoff sleep would restart the miner instead of
# quitting (bug the Rust sibling fixed first). An untrapped HUP (closed
# session) would skip the EXIT trap and leak the app-global wake-lock.
on_interrupt() {
  release_lock
  exit 130
}
trap release_lock EXIT
trap on_interrupt INT TERM HUP
if command -v termux-wake-lock >/dev/null 2>&1; then
  if timeout 5 termux-wake-lock >/dev/null 2>&1; then
    WAKE_LOCKED=1
    info "Wake-lock acquired (Android Doze will not suspend the miner)."
  else
    warn "Could not acquire wake-lock. Android Doze may pause the miner in background."
  fi
else
  note "termux-api not installed; Android Doze may pause the miner in background."
  note "Enable wake-locks with: pkg install termux-api"
fi

# ---- launch ----------------------------------------------------------------
printf "\n"
printf "  Version:  %s\n" "$BINARY_VERSION"
printf "  Pool:     %s\n" "$(jq -r '.["daemon-address"]' config.json)"
printf "  Wallet:   %s\n" "$(jq -r '.wallet' config.json)"
printf "  Threads:  %s\n" "$(jq -r '.threads' config.json)"
printf "\n"
info "Starting miner... (Ctrl-C to stop)"

BACKOFF=5
MAX_BACKOFF=30
FAST_FAILS=0
MAX_FAST_FAILS=5
while true; do
  START_TS="$(date +%s)"
  set +e
  "./$BINARY_NAME"
  CODE=$?
  set -e
  if [ "$CODE" -eq 0 ]; then
    info "Miner exited cleanly."
    break
  fi
  if [ "$(($(date +%s) - START_TS))" -ge 60 ]; then
    # A run that survived a minute was healthy; start the counters over.
    BACKOFF=5
    FAST_FAILS=0
  else
    FAST_FAILS=$((FAST_FAILS + 1))
  fi
  # The miner reconnects internally; an instant exit is essentially never
  # transient, so don't hammer it all night on a sleeping user's battery.
  if [ "$FAST_FAILS" -ge "$MAX_FAST_FAILS" ]; then
    err "Miner exited $FAST_FAILS times in under a minute each; giving up."
    note "Check the output above; --reconfigure rebuilds config.json."
    exit 1
  fi
  warn "Miner exited with code $CODE. Restarting in ${BACKOFF}s..."
  sleep "$BACKOFF"
  BACKOFF=$((BACKOFF * 2))
  [ "$BACKOFF" -gt "$MAX_BACKOFF" ] && BACKOFF=$MAX_BACKOFF
done
