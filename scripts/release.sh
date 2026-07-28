#!/usr/bin/env bash
# Builds the full release asset set into dist/ (family layout, same asset
# names as the Zig miner): usage  scripts/release.sh vX.Y.Z
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:?usage: scripts/release.sh vX.Y.Z}"
[[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  echo "version must match vX.Y.Z, got '$VERSION'" >&2
  exit 1
}
NAME="Dirtybird-Go-Miner"
LDFLAGS="-s -w -X main.version=${VERSION}"
PGO="-pgo=default.pgo"

rm -rf dist
mkdir -p dist

# plat  GOOS     GOARCH  GOAMD64  exe-suffix  launcher
# android-arm64 is the Termux asset: a LINUX-runtime PIE dressed for
# Android. GOOS=android is a trap without cgo — its runtime needs cgo for
# the g-register TLS setup (golang.org/issue/31343) and hangs at startup
# on real devices. Instead: linux runtime (pure-Go proven), -buildmode=pie
# because Android execs Termux binaries via bionic's linker64 which
# refuses ET_EXEC, PT_INTERP pointed at linker64, and PT_TLS realigned to
# 64 (bionic aborts below that). qemu cannot exercise any of this — the
# ELF gates in CI and the release smoke are the regression guards.
matrix() {
  echo "win64         windows amd64 v3 .exe start.bat"
  echo "amd64         linux   amd64 v3 ''   script.sh"
  echo "arm64         linux   arm64 -  ''   script.sh"
  echo "android-arm64 linux   arm64 -  ''   script.sh"
  echo "macos-arm64   darwin  arm64 -  ''   script.sh"
}

matrix | while read -r plat goos goarch amd64 suffix launcher; do
  [ "$suffix" = "''" ] && suffix=""
  stage="dist/${NAME}-${plat}-${VERSION}"
  mkdir -p "$stage"
  echo "== ${plat} (${goos}/${goarch}) =="
  buildmode=""
  ldextra=""
  if [ "$plat" = "android-arm64" ]; then
    buildmode="-buildmode=pie"
    ldextra=" -I /system/bin/linker64"
  fi
  env CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      $( [ "$amd64" = "v3" ] && echo "GOAMD64=v3" ) \
      go build -mod=readonly $PGO $buildmode -trimpath -ldflags "$LDFLAGS$ldextra" -o "${stage}/go-miner${suffix}" .
  if [ "$plat" = "android-arm64" ]; then
    python3 scripts/patch-tls-align.py "${stage}/go-miner"
  fi
  cp config.json README.md LICENSE THIRD-PARTY-LICENSES "$stage/"
  cp internal/astrobwt/LICENSE-DERO.txt "$stage/"
  cp "$launcher" "$stage/"
  case "$plat" in
  arm64|android-arm64)
    # Termux users get the installer offline too.
    cp scripts/termux-setup.sh "$stage/"
    chmod 0755 "$stage/termux-setup.sh"
    ;;
  esac
  if [ "$goos" = "windows" ]; then
    (cd dist && zip -qr "${NAME}-${plat}-${VERSION}.zip" "${NAME}-${plat}-${VERSION}")
  else
    chmod 0755 "${stage}/go-miner" "${stage}/${launcher}"
    tar -czf "dist/${NAME}-${plat}-${VERSION}.tar.gz" -C dist "${NAME}-${plat}-${VERSION}"
  fi
  rm -rf "$stage"
done

# HiveOS / MMPOS custom-miner package (linux amd64 binary + h-scripts).
# Hive derives the install directory from the archive name, so these must match.
HIVE_NAME="dirtybird-go-miner"
hasset="${HIVE_NAME}-${VERSION}.hiveos_mmpos.amd64.tar.gz"
hbase="${hasset%.tar.gz}"
hversion="${hbase##*-}"
hminer="${hbase%-$hversion}"
[ "$hminer" = "$HIVE_NAME" ] || {
  echo "Hive archive name resolves to '$hminer', expected '$HIVE_NAME'" >&2
  exit 1
}

hstage="dist/${HIVE_NAME}"
mkdir -p "$hstage"
env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v3 \
    go build -mod=readonly $PGO -trimpath -ldflags "$LDFLAGS" -o "${hstage}/go-miner" .
cp config/h-manifest.conf config/h-config.sh config/h-run.sh config/h-stats.sh "$hstage/"
sed -i "s/^CUSTOM_VERSION=.*/CUSTOM_VERSION=${VERSION#v}/" "${hstage}/h-manifest.conf"
chmod 0755 "${hstage}/go-miner" "${hstage}"/h-*.sh
tar -czf "dist/${hasset}" -C dist "$HIVE_NAME"
rm -rf "$hstage"

(cd dist && sha256sum ./*.zip ./*.tar.gz | sed 's|\./||' > SHA256SUMS.txt)
echo "release assets:"
cat dist/SHA256SUMS.txt
