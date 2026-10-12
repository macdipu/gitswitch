#!/bin/sh
# Builds both GitSwitch programs into dist/, plus dist/SHA256SUMS.
#   ./build.sh                     version "dev", every target this machine can build
#   ./build.sh v1.2.0              version stamped into the programs
#   ./build.sh v1.2.0 linux/arm64  only the targets named
#
# Each target gets two files:
#   gitswitch-<os>-<cpu>       the git hook, CLI and terminal app. Static, no
#                              cgo, so it builds anywhere and starts fast on
#                              every clone, pull and push.
#   gitswitch-app-<os>-<cpu>   the desktop app (Wails). It needs cgo on macOS
#                              and Linux, so those build only on their own OS
#                              (macOS builds both CPUs; Linux builds its own
#                              CPU and needs libgtk-3-dev and
#                              libwebkit2gtk-4.1-dev). Windows builds anywhere.
# GITSWITCH_NO_WINDOW=1 builds only gitswitch-*, for every target.
set -e
VERSION="${1:-dev}"
[ $# -gt 0 ] && shift
host_os=$(go env GOOS)
host_arch=$(go env GOARCH)

TARGETS="$*"
if [ -z "$TARGETS" ]; then
  if [ -n "$GITSWITCH_NO_WINDOW" ]; then
    TARGETS="windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64"
  else
    case $host_os in
      darwin) TARGETS="darwin/amd64 darwin/arm64 windows/amd64 windows/arm64" ;;
      linux)  TARGETS="linux/$host_arch windows/amd64 windows/arm64" ;;
      *)      TARGETS="windows/amd64 windows/arm64" ;;
    esac
  fi
fi

LDFLAGS="-s -w -X github.com/macdipu/gitswitch/internal/bootstrap.Version=$VERSION"
build() { # build <os> <arch> <cgo> <tags> <extra ldflags> <package> <output>
  CGO_ENABLED=$3 GOOS=$1 GOARCH=$2 go build -trimpath -tags "$4" -ldflags="$LDFLAGS $5" -o "$7" "$6"
  echo "built $7"
}

rm -rf dist && mkdir -p dist
for t in $TARGETS; do
  os=${t%/*}; arch=${t#*/}; ext=""; [ "$os" = windows ] && ext=".exe"
  build "$os" "$arch" 0 "" "" ./cmd/gitswitch "dist/gitswitch-$os-$arch$ext"
  [ -n "$GITSWITCH_NO_WINDOW" ] && continue
  app="dist/gitswitch-app-$os-$arch$ext"
  case $os in
    windows) build "$os" "$arch" 0 "desktop,production" "-H windowsgui" ./cmd/gitswitch-app "$app" ;;
    darwin|linux)
      if [ "$os" != "$host_os" ] || { [ "$os" = linux ] && [ "$arch" != "$host_arch" ]; }; then
        echo "build.sh: $t needs its own OS and CPU for the desktop app (or set GITSWITCH_NO_WINDOW=1)" >&2
        exit 1
      fi
      tags="desktop,production"; [ "$os" = linux ] && tags="$tags,webkit2_41"
      build "$os" "$arch" 1 "$tags" "" ./cmd/gitswitch-app "$app"
      # Install scripts before the app was split out download the -cli
      # name on Linux. Keep it for them for now.
      [ "$os" = linux ] && cp "dist/gitswitch-$os-$arch" "dist/gitswitch-$os-$arch-cli"
      ;;
  esac
done
cd dist && (sha256sum gitswitch-* 2>/dev/null || shasum -a 256 gitswitch-*) > SHA256SUMS && echo "wrote dist/SHA256SUMS"
