#!/usr/bin/env bash
# Smoke tests of the files of a release (section 9 of the spec):
#   scripts/smoke.sh bin/<stamp>-<version>
# The binary of this machine is run: commands, MCP and JSON-RPC, and the
# window when there is a display (or Xvfb). The others are checked without
# running: their format and architecture, the version inside, the content
# of the packages, the checksums.
set -uo pipefail
DIR="${1:?usage: scripts/smoke.sh RELEASE_DIR}"
DIR="$(cd "$DIR" && pwd)"
FAIL=0
ok()   { echo "  ok   $*"; }
bad()  { echo "  FAIL $*"; FAIL=1; }
check() { # check DESCRIPTION COMMAND...
  local what="$1"; shift
  if "$@" >/dev/null 2>&1; then ok "$what"; else bad "$what"; fi
}
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "== checksums"
if [ -f "$DIR/SHA256SUMS" ]; then
  check "SHA256SUMS match" bash -c "cd '$DIR' && sha256sum -c --quiet SHA256SUMS"
else
  bad "no SHA256SUMS"
fi

VERSION="$(ls "$DIR" | sed -n -E 's/^althex-(.*)-linux-amd64\.tar\.gz$/\1/p' | head -1)"
echo "== version: ${VERSION:-unknown}"

echo "== formats of the binaries"
fmt() { # fmt FILE PATTERN
  [ -f "$DIR/$1" ] || { bad "$1 missing"; return; }
  if file -b "$DIR/$1" | grep -Eq "$2"; then ok "$1: $(file -b "$DIR/$1" | cut -c1-60)"; else bad "$1: $(file -b "$DIR/$1")"; fi
  if [ -n "$VERSION" ] && grep -aq -- "$VERSION" "$DIR/$1"; then ok "$1 has version $VERSION"; else bad "$1 lacks version $VERSION"; fi
}
fmt althex-linux-amd64 'ELF 64-bit.*x86-64'
fmt althex-linux-arm64 'ELF 64-bit.*(aarch64|ARM aarch64)'
fmt althex.exe 'PE32\+ executable.*(GUI|x86-64)'
fmt althex-darwin-arm64 'Mach-O 64-bit.*arm64'

echo "== packages"
for arch in amd64 arm64; do
  t="$DIR/althex-$VERSION-linux-$arch.tar.gz"
  check "tar $arch: althex, icon, NOTICES" bash -c "l=\$(tar tzf '$t'); for f in althex althex.svg NOTICES.txt; do grep -qx \$f <<<\"\$l\" || exit 1; done"
  if command -v dpkg-deb >/dev/null; then
    check "deb $arch: binary and NOTICES" bash -c "dpkg-deb -c '$DIR/althex-linux-$arch.deb' | grep -q usr/bin/althex && dpkg-deb -c '$DIR/althex-linux-$arch.deb' | grep -q usr/share/doc/althex/NOTICES"
  fi
  if command -v 7z >/dev/null; then
    check "rpm $arch: readable" 7z l "$DIR/althex-linux-$arch.rpm"
  fi
done
if command -v isoinfo >/dev/null && [ -f "$DIR/althex-darwin-arm64.dmg" ]; then
  # Made by hdiutil on macOS (UDZO) or as an ISO image elsewhere
  if isoinfo -R -f -i "$DIR/althex-darwin-arm64.dmg" >"$WORK/dmg.txt" 2>/dev/null; then
    check "dmg: the app and NOTICES" bash -c "grep -q 'AltHex.app/Contents/MacOS/althex' '$WORK/dmg.txt' && grep -q 'Resources/NOTICES.txt' '$WORK/dmg.txt'"
  else
    ok "dmg: not an ISO image (hdiutil), not looked into"
  fi
fi

BIN=""
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64) BIN="$DIR/althex-linux-amd64" ;;
  Linux/aarch64) BIN="$DIR/althex-linux-arm64" ;;
  Darwin/arm64) BIN="$DIR/althex-darwin-arm64" ;;
esac
if [ -n "$BIN" ]; then
  echo "== running $(basename "$BIN")"
  export HOME="$WORK/home"
  mkdir -p "$HOME"
  head -c 100000 /dev/urandom > "$WORK/data.bin"
  printf 'Hello, AltHex' >> "$WORK/data.bin"
  check "version" bash -c "'$BIN' version | grep -q AltHex"
  check "help" bash -c "'$BIN' help | grep -q 'althex mcp'"
  check "licenses" bash -c "'$BIN' licenses | grep -q 'Apache License'"
  want="$(sha256sum "$WORK/data.bin" | cut -d' ' -f1)"
  check "hash = sha256sum" bash -c "'$BIN' hash -a sha256 '$WORK/data.bin' | grep -q $want"
  check "find text" bash -c "'$BIN' find -text 'AltHex' '$WORK/data.bin' | grep -qi 0x186A7"
  req='{"jsonrpc":"2.0","id":1,"method":"read","params":{"offset":100000,"length":5,"format":"text"}}'
  check "rpc read" bash -c "echo '$req' | timeout 20 '$BIN' rpc -headless '$WORK/data.bin' | grep -q Hello"
  init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}'
  call='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"hash","arguments":{"algorithms":["sha256"]}}}'
  check "mcp hash" bash -c "printf '%s\n%s\n' '$init' '$call' | timeout 20 '$BIN' mcp -headless '$WORK/data.bin' | grep -q $want"
  # The window: on the display there is, else on a virtual one
  disp="${DISPLAY:-}"
  xvfb=""
  if [ "$(uname -s)" = Linux ] && command -v Xvfb >/dev/null; then
    Xvfb :97 -screen 0 1280x800x24 >/dev/null 2>&1 &
    xvfb=$!
    disp=":97"
    sleep 1
  fi
  if [ -n "$disp" ] || [ "$(uname -s)" = Darwin ]; then
    DISPLAY="$disp" "$BIN" "$WORK/data.bin" >"$WORK/gui.log" 2>&1 &
    gui=$!
    sleep 4
    if kill -0 "$gui" 2>/dev/null; then ok "window runs"; kill "$gui"; wait "$gui" 2>/dev/null; else bad "window exited: $(tail -3 "$WORK/gui.log")"; fi
  else
    ok "window: no display, not started"
  fi
  [ -n "$xvfb" ] && kill "$xvfb" 2>/dev/null
fi

if [ "$FAIL" = 0 ]; then echo "SMOKE: all passed"; else echo "SMOKE: FAILED"; fi
exit "$FAIL"
