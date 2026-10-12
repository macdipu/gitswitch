#!/bin/sh
# Regenerates the desktop app screenshots in docs/images from demo data, so
# every picture shows the same version and the same made-up accounts.
#   test/screenshots/run.sh [version]     (needs Docker; run from anywhere)
# The demo (demo/main.go) serves the real page and API with a throwaway
# home folder; Playwright's Chromium takes the pictures.
set -e
cd "$(dirname "$0")/../.."
VERSION="${1:-$(git describe --tags --abbrev=0 2>/dev/null || echo dev)}"
PW=1.48.2
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

docker run --rm -v "$PWD":/src -v "$TMP":/out -w /src -e CGO_ENABLED=0 golang:1.27 \
  go build -buildvcs=false -ldflags "-X github.com/macdipu/gitswitch/internal/bootstrap.Version=$VERSION" -o /out/demo ./test/screenshots/demo

docker run --rm --ipc=host -v "$PWD":/src -v "$TMP":/out -w /out mcr.microsoft.com/playwright:v$PW-noble sh -c "
  set -e
  apt-get -qq update >/dev/null && apt-get -qq install -y git openssh-client >/dev/null
  # A real user: ssh finds ~/.ssh through the user's entry, not \$HOME.
  useradd -m -d /home/alex alex && cp demo /home/alex/.demo
  su alex -c '/home/alex/.demo /home/alex 127.0.0.1:8765' >demo.log 2>&1 &
  npm init -y >/dev/null && npm i -s playwright@$PW >/dev/null
  for i in \$(seq 50); do grep -q 'demo GitSwitch' demo.log 2>/dev/null && break; sleep 0.2; done
  cat demo.log
  cp /src/test/screenshots/shoot.mjs . && node shoot.mjs http://127.0.0.1:8765 /src/docs/images
"
echo "Updated docs/images/web-*-{light,dark}.png ($VERSION)"
