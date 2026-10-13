#!/bin/bash
# rebuild_chrome_argus.sh — rebuild /Applications/Chrome Argus.app from a fresh
# Chrome install (argus#18). Re-run after each Chrome update; the stub resolves
# Versions/* dynamically so no version strings are hardcoded.
#
#   ./rebuild_chrome_argus.sh [--source "/Applications/Google Chrome.app"] [--dest "/Applications/Chrome Argus.app"]
#
# What it does:
#   1. Copy the source .app to the destination (rsync, replaces stale bits).
#   2. Compile chrome_argus_stub.c → Contents/MacOS/chrome-argus.bin (universal x86_64+arm64).
#   3. Write the chrome-argus wrapper (profile dir + CDP 9224 defaults).
#   4. Rewrite Info.plist: bundle id com.tangtszho.chrome-argus, name, executable.
#   5. Ad-hoc re-sign the stub only (arm64 requires a signature to exec).
# Keeps the separate Dock identity + separate TCC permission records.
set -euo pipefail

SRC="/Applications/Google Chrome.app"
DST="/Applications/Chrome Argus.app"
BUNDLE_ID="com.tangtszho.chrome-argus"
HERE="$(cd "$(dirname "$0")" && pwd)"

while [ $# -gt 0 ]; do
  case "$1" in
    --source) SRC="$2"; shift 2 ;;
    --dest)   DST="$2"; shift 2 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

[ -d "$SRC" ] || { echo "source app not found: $SRC" >&2; exit 1; }
[ -f "$HERE/chrome_argus_stub.c" ] || { echo "stub source missing: $HERE/chrome_argus_stub.c" >&2; exit 1; }

echo "== syncing $SRC -> $DST"
mkdir -p "$DST"
rsync -a --delete "$SRC/" "$DST/"

echo "== compiling stub"
TMPBIN="$(mktemp -t chrome-argus-stub)"
clang -O2 -arch x86_64 -arch arm64 -o "$TMPBIN" "$HERE/chrome_argus_stub.c"
mkdir -p "$DST/Contents/MacOS"
mv "$TMPBIN" "$DST/Contents/MacOS/chrome-argus.bin"
chmod +x "$DST/Contents/MacOS/chrome-argus.bin"

echo "== writing wrapper"
cat << 'EOF' > "$DST/Contents/MacOS/chrome-argus"
#!/bin/bash
exec "/Applications/Chrome Argus.app/Contents/MacOS/chrome-argus.bin" \
  --user-data-dir="$HOME/.config/argus/chrome-profile" \
  --remote-debugging-port=9224 \
  --no-first-run \
  --disable-default-apps \
  --no-default-browser-check \
  "$@"
EOF
chmod +x "$DST/Contents/MacOS/chrome-argus"

echo "== rewriting Info.plist"
PLIST="$DST/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier $BUNDLE_ID" "$PLIST" 2>/dev/null \
  || /usr/libexec/PlistBuddy -c "Add :CFBundleIdentifier string $BUNDLE_ID" "$PLIST"
/usr/libexec/PlistBuddy -c "Set :CFBundleExecutable chrome-argus" "$PLIST" 2>/dev/null \
  || /usr/libexec/PlistBuddy -c "Add :CFBundleExecutable string chrome-argus" "$PLIST"
for key in CFBundleName CFBundleDisplayName; do
  /usr/libexec/PlistBuddy -c "Set :$key 'Chrome Argus'" "$PLIST" 2>/dev/null \
    || /usr/libexec/PlistBuddy -c "Add :$key string 'Chrome Argus'" "$PLIST"
done

echo "== ad-hoc signing stub"
codesign --force --sign - "$DST/Contents/MacOS/chrome-argus.bin"
# Chrome's own binaries keep their original signatures; only our stub is new code.

echo "== sanity check"
VER_JSON="$("$DST/Contents/MacOS/chrome-argus.bin" --version 2>/dev/null || true)"
echo "stub --version: ${VER_JSON:-<no output>}"
echo "done: $DST (bundle id $BUNDLE_ID, exec chrome-argus)"
echo "note: Dock 点击即启动需 chrome-argus wrapper；若已钉旧图标，重新拖入 Dock。"
