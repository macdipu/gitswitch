#!/bin/sh
# Builds gitswitch for every supported platform into dist/, plus dist/SHA256SUMS.
#   ./build.sh            version "dev"
#   ./build.sh v1.2.0     version stamped into the binaries
set -e
VERSION="${1:-dev}"
rm -rf dist && mkdir -p dist
for t in windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
  os=${t%/*}; arch=${t#*/}; ext=""; [ "$os" = windows ] && ext=".exe"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
    -ldflags="-s -w -X gitswitch/internal/app.Version=$VERSION" \
    -o "dist/gitswitch-$os-$arch$ext" ./cmd/gitswitch
  echo "built dist/gitswitch-$os-$arch$ext"
done
cd dist && (sha256sum gitswitch-* 2>/dev/null || shasum -a 256 gitswitch-*) > SHA256SUMS && echo "wrote dist/SHA256SUMS"
