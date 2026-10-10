#!/bin/sh
# GitSwitch one-line installer for macOS, Linux, WSL and Git Bash on Windows.
#   curl -fsSL https://raw.githubusercontent.com/OWNER/REPO/main/install.sh | sh
# Options (environment variables):
#   GITSWITCH_VERSION=v1.2.0   install a specific release instead of the latest
#   GITSWITCH_REPO=owner/repo  install from a fork
set -eu

REPO="${GITSWITCH_REPO:-OWNER/gitswitch}"   # <-- set to your GitHub repo before publishing
VERSION="${GITSWITCH_VERSION:-latest}"

say()  { printf '%s\n' "$*"; }
fail() { printf 'gitswitch install: %s\n' "$*" >&2; exit 1; }

# ---- detect OS ----
case "$(uname -s)" in
  Linux)                 OS=linux ;;
  Darwin)                OS=darwin ;;
  MINGW*|MSYS*|CYGWIN*)  OS=windows ;;
  *) fail "unsupported system: $(uname -s). Download a file from https://github.com/$REPO/releases" ;;
esac

# ---- detect CPU ----
case "$(uname -m)" in
  x86_64|amd64)  ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) fail "unsupported CPU: $(uname -m)" ;;
esac
# A Terminal running under Rosetta reports x86_64 on Apple Silicon Macs.
if [ "$OS" = darwin ] && [ "$ARCH" = amd64 ] && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || echo 0)" = 1 ]; then
  ARCH=arm64
fi

EXT=""; [ "$OS" = windows ] && EXT=".exe"
ASSET="gitswitch-$OS-$ARCH$EXT"

if [ -n "${GITSWITCH_BASE_URL:-}" ]; then
  BASE="$GITSWITCH_BASE_URL"                                   # testing / mirrors
elif [ "$VERSION" = latest ]; then
  BASE="https://github.com/$REPO/releases/latest/download"
else
  BASE="https://github.com/$REPO/releases/download/$VERSION"
fi

# ---- download helpers ----
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --retry 2 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  fail "curl or wget is required"
fi

command -v git >/dev/null 2>&1 || fail "git is not installed. Install git first, then run this again."

TMP="$(mktemp -d 2>/dev/null || mktemp -d -t gitswitch)"
trap 'rm -rf "$TMP"' EXIT INT TERM

say "Downloading $ASSET ($VERSION)..."
fetch "$BASE/$ASSET" "$TMP/$ASSET" || fail "download failed: $BASE/$ASSET"
fetch "$BASE/SHA256SUMS" "$TMP/SHA256SUMS" || fail "could not download SHA256SUMS"

# ---- verify checksum ----
EXPECTED="$(grep " $ASSET\$" "$TMP/SHA256SUMS" | awk '{print $1}' | head -n 1)"
[ -n "$EXPECTED" ] || fail "no checksum for $ASSET in SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "$TMP/$ASSET" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL="$(shasum -a 256 "$TMP/$ASSET" | awk '{print $1}')"
else
  fail "sha256sum or shasum is needed to verify the download"
fi
[ "$ACTUAL" = "$EXPECTED" ] || fail "checksum mismatch for $ASSET (expected $EXPECTED, got $ACTUAL). Nothing was installed."
say "Checksum OK."

chmod +x "$TMP/$ASSET"
if [ "$OS" = darwin ]; then
  xattr -d com.apple.quarantine "$TMP/$ASSET" 2>/dev/null || true
fi

# ---- install: the program copies itself to its fixed folder and hooks into git ----
"$TMP/$ASSET" install

say ""
say "Done. Open a new terminal, then run:"
say "  gitswitch        terminal app"
say "  gitswitch ui     browser app"
say "  gitswitch setup  step-by-step setup"
