#!/bin/sh
# Records the terminal GIFs in docs/images (setup.gif, tui.gif) with VHS,
# from made-up accounts, so they match the current program.
#   test/screenshots/gifs.sh [version]     (needs Docker; run from anywhere)
# A fake ssh (fakessh) answers the login test like GitHub; nothing goes
# over the network.
set -e
cd "$(dirname "$0")/../.."
VERSION="${1:-$(git describe --tags --abbrev=0 2>/dev/null || echo dev)}"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

docker run --rm -v "$PWD":/src -v "$TMP":/out -w /src -e CGO_ENABLED=0 golang:1.27 sh -c "
  go build -buildvcs=false -ldflags '-s -w -X github.com/macdipu/gitswitch/internal/bootstrap.Version=$VERSION' -o /out/gitswitch ./cmd/gitswitch &&
  go build -buildvcs=false -o /out/demo ./test/screenshots/demo"

docker run --rm -v "$PWD":/src -v "$TMP":/out -w /out --entrypoint sh ghcr.io/charmbracelet/vhs:v0.8.0 -c '
  set -e
  # The image is pinned; Debian "stable" has moved on since, so accept that.
  apt-get -qq update --allow-releaseinfo-change >/dev/null 2>&1 || true
  apt-get -qq install -y git openssh-client >/dev/null
  command -v git >/dev/null && command -v ssh-keygen >/dev/null || { echo "gifs.sh: git or ssh missing in the VHS image" >&2; exit 1; }
  cp /out/gitswitch /src/test/screenshots/fakessh /usr/local/bin/
  # A real user: ssh finds ~/.ssh through the user entry, not $HOME.
  useradd -m -s /bin/bash alex
  vhs /src/test/screenshots/setup.tape
  userdel -r alex 2>/dev/null; useradd -m -s /bin/bash alex
  su alex -c "/out/demo /home/alex -" && cp /out/gitswitch /home/alex/.local/bin/gitswitch
  vhs /src/test/screenshots/tui.tape
  cp setup.gif tui.gif /src/docs/images/
'
echo "Updated docs/images/setup.gif and tui.gif ($VERSION)"
