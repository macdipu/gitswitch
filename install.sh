#!/bin/sh
# GitSwitch one-line installer for macOS, Linux, WSL and Git Bash on Windows.
#   curl -fsSL https://raw.githubusercontent.com/macdipu/gitswitch/main/install.sh | sh
# Options (environment variables):
#   GITSWITCH_VERSION=v1.2.0   install a specific release instead of the latest
#   GITSWITCH_REPO=owner/repo  install from a fork
#   GITSWITCH_CLI_ONLY=1       Linux: install the build without the desktop window
set -eu

REPO="${GITSWITCH_REPO:-macdipu/gitswitch}"
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
# On Linux the desktop window needs WebKitGTK, and a program linked to a
# missing library cannot start at all (git runs gitswitch for every pull and
# push). Without WebKitGTK, install the static build that has no window.
has_webkit() {
  { PATH="$PATH:/sbin:/usr/sbin" ldconfig -p 2>/dev/null | grep -q 'libwebkit2gtk-4\.1\.so\.0'; } ||
    for f in /usr/lib/libwebkit2gtk-4.1.so.0 /usr/lib*/libwebkit2gtk-4.1.so.0 /usr/lib/*/libwebkit2gtk-4.1.so.0; do
      [ -e "$f" ] && return 0
    done
  return 1
}
if [ "$OS" = linux ] && { [ -n "${GITSWITCH_CLI_ONLY:-}" ] || ! has_webkit; }; then
  ASSET="$ASSET-cli"
fi

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

fetch "$BASE/SHA256SUMS" "$TMP/SHA256SUMS" || fail "could not download SHA256SUMS"

# ---- download and verify one file ----
get() {
  say "Downloading $1 ($VERSION)..."
  fetch "$BASE/$1" "$TMP/$1" || fail "download failed: $BASE/$1"
  EXPECTED="$(grep " $1\$" "$TMP/SHA256SUMS" | awk '{print $1}' | head -n 1)"
  [ -n "$EXPECTED" ] || fail "no checksum for $1 in SHA256SUMS"
  if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL="$(sha256sum "$TMP/$1" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    ACTUAL="$(shasum -a 256 "$TMP/$1" | awk '{print $1}')"
  else
    fail "sha256sum or shasum is needed to verify the download"
  fi
  [ "$ACTUAL" = "$EXPECTED" ] || fail "checksum mismatch for $1 (expected $EXPECTED, got $ACTUAL). Nothing was installed."
  say "Checksum OK."
  chmod +x "$TMP/$1"
  if [ "$OS" = darwin ]; then
    xattr -d com.apple.quarantine "$TMP/$1" 2>/dev/null || true
  fi
}
starts() { "$TMP/$1" version >/dev/null 2>&1; }

get "$ASSET"
# Make sure the program starts here before git depends on it: git runs it
# on every pull and push. A Linux desktop build can fail to start when a
# library is missing or too old; the build without the window has none.
if ! starts "$ASSET"; then
  case "$ASSET" in
    gitswitch-linux-*-cli) fail "$ASSET does not start on this system. Nothing was installed." ;;
    gitswitch-linux-*)
      say "The desktop build does not start on this system; using the build without the window."
      ASSET="$ASSET-cli"
      get "$ASSET"
      starts "$ASSET" || fail "$ASSET does not start on this system. Nothing was installed."
      ;;
    *) fail "$ASSET does not start on this system. Nothing was installed." ;;
  esac
fi

# ---- install: the program copies itself to its fixed folder and hooks into git ----
"$TMP/$ASSET" install

say ""
say "Done. Open a new terminal, then run:"
say "  gitswitch        terminal app"
case "$ASSET" in
  *-cli) say "  (no desktop window: install libwebkit2gtk-4.1-0 and run this again for \`gitswitch ui\`)" ;;
  *)     say "  gitswitch ui     desktop app" ;;
esac
say "  gitswitch setup  step-by-step setup"
