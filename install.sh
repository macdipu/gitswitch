#!/bin/sh
# GitSwitch one-line installer for macOS, Linux, WSL and Git Bash on Windows.
#   curl -fsSL https://raw.githubusercontent.com/macdipu/gitswitch/main/install.sh | sh
# Options (environment variables):
#   GITSWITCH_VERSION=v1.2.0   install a specific release instead of the latest
#   GITSWITCH_REPO=owner/repo  install from a fork
#   GITSWITCH_CLI_ONLY=1       install only the terminal programs, not the desktop app
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
ASSET="gitswitch-$OS-$ARCH$EXT"         # git hook, CLI and terminal app
APP_ASSET="gitswitch-app-$OS-$ARCH$EXT" # desktop app
# On Linux the desktop app needs WebKitGTK; without it, install only the
# terminal programs.
has_webkit() {
  PATH="$PATH:/sbin:/usr/sbin" ldconfig -p 2>/dev/null | grep -q 'libwebkit2gtk-4\.1\.so\.0' && return 0
  for f in /usr/lib/libwebkit2gtk-4.1.so.0 /usr/lib*/libwebkit2gtk-4.1.so.0 /usr/lib/*/libwebkit2gtk-4.1.so.0; do
    [ -e "$f" ] && return 0
  done
  return 1
}
WANT_APP=1
if [ -n "${GITSWITCH_CLI_ONLY:-}" ]; then
  WANT_APP=""
elif [ "$OS" = linux ] && ! has_webkit; then
  WANT_APP=""
  say "WebKitGTK 4.1 is not installed, so the desktop app is skipped (install libwebkit2gtk-4.1-0 and run this again to add it)."
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

# ---- download and verify one file: get <asset> <name to save as> ----
# Returns 1 when the release has no such file (an older release).
get() {
  EXPECTED="$(grep " $1\$" "$TMP/SHA256SUMS" | awk '{print $1}' | head -n 1)"
  [ -n "$EXPECTED" ] || return 1
  say "Downloading $1 ($VERSION)..."
  fetch "$BASE/$1" "$TMP/bin/$2" || fail "download failed: $BASE/$1"
  if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL="$(sha256sum "$TMP/bin/$2" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    ACTUAL="$(shasum -a 256 "$TMP/bin/$2" | awk '{print $1}')"
  else
    fail "sha256sum or shasum is needed to verify the download"
  fi
  [ "$ACTUAL" = "$EXPECTED" ] || fail "checksum mismatch for $1 (expected $EXPECTED, got $ACTUAL). Nothing was installed."
  say "Checksum OK."
  chmod +x "$TMP/bin/$2"
  if [ "$OS" = darwin ]; then
    xattr -d com.apple.quarantine "$TMP/bin/$2" 2>/dev/null || true
  fi
}
mkdir -p "$TMP/bin"

get "$ASSET" "gitswitch$EXT" || fail "no checksum for $ASSET in SHA256SUMS"
# Make sure the program starts here before git depends on it: git runs it
# on every clone, pull and push.
"$TMP/bin/gitswitch$EXT" version >/dev/null 2>&1 || fail "$ASSET does not start on this system. Nothing was installed."

HAVE_APP=""
if [ -n "$WANT_APP" ]; then
  if ! get "$APP_ASSET" "gitswitch-app$EXT"; then
    say "This release has no separate desktop app; installing the terminal programs."
  elif [ "$OS" = windows ] || "$TMP/bin/gitswitch-app$EXT" --version >/dev/null 2>&1; then
    HAVE_APP=1
  else
    # A library it needs is missing or too old. The terminal app still works.
    say "The desktop app does not start on this system; installing the terminal programs only."
    rm -f "$TMP/bin/gitswitch-app$EXT"
  fi
fi

# ---- install: gitswitch copies itself (and gitswitch-app next to it) to
# their fixed folders, hooks into git and adds the app to the app menu ----
"$TMP/bin/gitswitch$EXT" install

say ""
say "Done. Open a new terminal, then run:"
say "  gitswitch        terminal app"
if [ -n "$HAVE_APP" ]; then
  say "  gitswitch ui     desktop app (also in your applications menu)"
fi
say "  gitswitch setup  step-by-step setup"
