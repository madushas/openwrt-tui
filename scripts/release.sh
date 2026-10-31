#!/usr/bin/env bash
# Build release archives and a SHA256SUMS file in ./dist.
# usage: scripts/release.sh v0.1.0
set -euo pipefail
version="${1:?usage: scripts/release.sh <version>}"
cd "$(dirname "$0")/.."
rm -rf dist && mkdir -p dist
targets="linux/amd64 linux/arm64 linux/arm linux/mipsle darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"
for t in $targets; do
  os=${t%/*}; arch=${t#*/}
  name="owrt-tui_${version}_${os}_${arch}"
  bin="owrt-tui"; [ "$os" = windows ] && bin="owrt-tui.exe"
  stage="dist/$name"; mkdir -p "$stage"
  extra=""; [ "$arch" = arm ] && extra="GOARM=7"; [ "$arch" = mipsle ] && extra="GOMIPS=softfloat"
  env CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" $extra \
    go build -trimpath -ldflags "-s -w -X github.com/madushakv/owrt-tui/internal/cli.Version=${version}" \
    -o "$stage/$bin" ./cmd/owrt-tui
  cp README.md "$stage/"
  cp -r docs "$stage/docs"
  if [ "$os" = windows ]; then
    (cd dist && zip -qr "$name.zip" "$name")
  else
    tar -C dist -czf "dist/$name.tar.gz" "$name"
  fi
  rm -rf "$stage"
done
(cd dist && if command -v sha256sum >/dev/null; then sha256sum *.tar.gz *.zip; else shasum -a 256 *.tar.gz *.zip; fi > SHA256SUMS)
echo "built:"; ls -1 dist
