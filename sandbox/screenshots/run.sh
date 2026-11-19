#!/bin/sh
# Starts the demo container for the screenshots (see README.md here).
# Run from anywhere; it works out the repo root from this file's place.
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
docker rm -f gsshots >/dev/null 2>&1
docker run -d --name gsshots -p 127.0.0.1:8091:8091 \
  -e HOME=/home/dana -e GITSWITCH_TEST_NO_SYSTEM=1 \
  -v "$root":/src -v "$here":/shots \
  -v gomodcache:/go/pkg/mod -v gobuildcache:/root/.cache/go-build \
  -w /src golang:1.27 sh /shots/setup.sh
