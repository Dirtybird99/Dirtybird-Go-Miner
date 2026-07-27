#!/usr/bin/env bash
# Dirtybird Go Miner — Termux (Android) setup, ported from the sibling
# C miner's scripts/termux-setup.sh.
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
  if ! (pkg update -y >/dev/null 2>&1 && pkg install -y "${NEED_PKGS[@]}" >/dev/null 2>&1); then
    err "Package install failed. Run manually: pkg install -y ${NEED_PKGS[*]}"
    exit 1
  fi
fi

fetch() { # url -> stdout
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --connect-timeout 5 --max-time 10 "$1"
  else
    wget -qO- -T 10 "$1"
  fi
}
fetch_to() { # url file (longer budget: release binaries)
  if command -v curl >/dev/null 2>&1; then
    curl -fSL --connect-timeout 5 --max-time 300 -o "$2" "$1"
  else
    wget -q -T 300 -O "$2" "$1"
  fi
}
latest_release_tag() {
  fetch "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | jq -r '.tag_name // empty'
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
    err "Could not determine latest release. Check network connection."
    exit 1
  fi
  case "$LATEST_TAG" in
    v[0-9]*) ;;
    *) err "Unexpected release tag format: $LATEST_TAG"; exit 1 ;;
  esac
  ARCHIVE="${ARCHIVE_PREFIX}${LATEST_TAG}${ARCHIVE_SUFFIX}"
  URL="https://github.com/$REPO/releases/download/$LATEST_TAG/$ARCHIVE"
  info "Downloading $ARCHIVE..."
  fetch_to "$URL" "$ARCHIVE"
  # Never let a fresh marker sit next to a stale binary.
  rm -f "./$BINARY_NAME"
  find . -maxdepth 1 -type d -name "${ARCHIVE_PREFIX%?}*" -exec rm -rf {} +
  tar -xzf "$ARCHIVE"
  rm -f "$ARCHIVE"
  PKG_DIR="${ARCHIVE%$ARCHIVE_SUFFIX}"
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

  printf "${CYAN}DERO wallet address${NC}\n"
  printf "  Press Enter to use: %s\n" "$DEFAULT_WALLET"
  read -rp "  Wallet: " WALLET </dev/tty || WALLET=""
  WALLET="${WALLET:-$DEFAULT_WALLET}"
  if ! printf '%s' "$WALLET" | grep -qE '^(dero1|deto1)[a-z0-9]+$'; then
    err "Invalid wallet address: $WALLET"
    err "Must start with 'dero1' or 'deto1' followed by lowercase alphanumerics."
    exit 1
  fi

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

  cat > config.json <<EOF
{
  "daemon-address": "$POOL",
  "wallet": "$WALLET",
  "threads": $THREADS
}
EOF
  info "Config written to $INSTALL_DIR/config.json"
else
  info "Using existing config.json (use --reconfigure to change)."
fi

# ---- battery advisory ------------------------------------------------------
if command -v termux-battery-status >/dev/null 2>&1; then
  BATTERY_JSON="$(termux-battery-status 2>/dev/null || true)"
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
    termux-wake-unlock >/dev/null 2>&1 || true
    info "Wake-lock released."
  fi
}
trap release_lock EXIT INT TERM
if command -v termux-wake-lock >/dev/null 2>&1; then
  if termux-wake-lock >/dev/null 2>&1; then
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
  # A run that survived a minute was healthy; start the backoff over.
  if [ "$(($(date +%s) - START_TS))" -ge 60 ]; then
    BACKOFF=5
  fi
  warn "Miner exited with code $CODE. Restarting in ${BACKOFF}s..."
  sleep "$BACKOFF"
  BACKOFF=$((BACKOFF * 2))
  [ "$BACKOFF" -gt "$MAX_BACKOFF" ] && BACKOFF=$MAX_BACKOFF
done
