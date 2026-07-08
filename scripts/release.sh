#!/usr/bin/env bash
# Builds the full release asset set into dist/ (family layout, same asset
# names as the Zig miner): usage  scripts/release.sh v0.1.0
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:?usage: scripts/release.sh vX.Y.Z}"
NAME="Dirtybird-Go-Miner"
LDFLAGS="-s -w -X main.version=${VERSION}"
PGO="-pgo=default.pgo"

rm -rf dist
mkdir -p dist

# plat  GOOS     GOARCH  GOAMD64  exe-suffix  launcher
matrix() {
  echo "win64        windows amd64 v3 .exe start.bat"
  echo "amd64        linux   amd64 v3 ''   script.sh"
  echo "arm64        linux   arm64 -  ''   script.sh"
  echo "macos-arm64  darwin  arm64 -  ''   script.sh"
}

matrix | while read -r plat goos goarch amd64 suffix launcher; do
  [ "$suffix" = "''" ] && suffix=""
  stage="dist/${NAME}-${plat}-${VERSION}"
  mkdir -p "$stage"
  echo "== ${plat} (${goos}/${goarch}) =="
  env CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      $( [ "$amd64" = "v3" ] && echo "GOAMD64=v3" ) \
      go build $PGO -trimpath -ldflags "$LDFLAGS" -o "${stage}/go-miner${suffix}" .
  cp config.json README.md LICENSE THIRD-PARTY-LICENSES "$stage/"
  cp internal/astrobwt/LICENSE-DERO.txt "$stage/"
  cp "$launcher" "$stage/"
  if [ "$goos" = "windows" ]; then
    (cd dist && zip -qr "${NAME}-${plat}-${VERSION}.zip" "${NAME}-${plat}-${VERSION}")
  else
    tar -czf "dist/${NAME}-${plat}-${VERSION}.tar.gz" -C dist "${NAME}-${plat}-${VERSION}"
  fi
  rm -rf "$stage"
done

# HiveOS / MMPOS custom-miner package (linux amd64 binary + h-scripts)
hstage="dist/go-miner"
mkdir -p "$hstage"
env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v3 \
    go build $PGO -trimpath -ldflags "$LDFLAGS" -o "${hstage}/go-miner" .
cp config/h-manifest.conf config/h-config.sh config/h-run.sh config/h-stats.sh "$hstage/"
sed -i "s/^CUSTOM_VERSION=.*/CUSTOM_VERSION=${VERSION#v}/" "${hstage}/h-manifest.conf"
tar -czf "dist/dirtybird-go-miner-${VERSION}.hiveos_mmpos.amd64.tar.gz" -C dist go-miner
rm -rf "$hstage"

(cd dist && sha256sum ./*.zip ./*.tar.gz | sed 's|\./||' > SHA256SUMS.txt)
echo "release assets:"
cat dist/SHA256SUMS.txt
