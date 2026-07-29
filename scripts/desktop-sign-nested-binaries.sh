#!/usr/bin/env bash
# Sign the packaged Multica.app and — critically — its nested Go binaries,
# then verify they actually execute.
#
# WHY THIS EXISTS (0.3.66, closes a recurring ship-blocker):
# `electron-builder --mac --dir` only ad-hoc-signs the TOP-LEVEL .app bundle.
# The bundled Go binaries at
#   <app>/Contents/Resources/app.asar.unpacked/resources/bin/{multica,server,migrate}
# keep only the ad-hoc signature bundle-cli gave them, which macOS 27's
# Gatekeeper treats as bundle parts and rejects: any fork+exec of them is
# SIGKILL'd (exit 137, namespace=CODESIGNING). Symptom: the GUI cold-starts
# but `multica --help` returns 137, the daemon can't spawn its CLI, and the
# server never binds :8090. Copying a binary to /tmp and running it there
# works — the kill only fires from inside app.asar.unpacked/.
#
# The fix is to re-sign each nested binary individually AFTER the .app is
# installed (signing before `cp -R` is useless — the copy invalidates it).
# This used to be a doc-only, manual step in CLAUDE.md and was forgotten on
# more than one ship; this script makes it runnable, idempotent, and
# self-verifying so the regression can't silently recur.
#
# Usage:
#   bash scripts/desktop-sign-nested-binaries.sh [path-to-Multica.app]
# Defaults to /Applications/Multica.app. Pass dist/mac-arm64/Multica.app to
# sign a build output in place (e.g. before a manual copy). Safe to re-run.
set -euo pipefail

APP="${1:-/Applications/Multica.app}"
BIN_DIR="$APP/Contents/Resources/app.asar.unpacked/resources/bin"
BINS=(multica server migrate)

if [ ! -d "$APP" ]; then
  echo "✗ app not found: $APP" >&2
  exit 1
fi
if ! command -v codesign >/dev/null 2>&1; then
  echo "✗ 'codesign' not found (this script is macOS-only)" >&2
  exit 1
fi

echo "==> signing app bundle (ad-hoc, --deep): $APP"
codesign --force --deep --sign - "$APP"

if [ ! -d "$BIN_DIR" ]; then
  echo "✗ nested binary dir not found: $BIN_DIR" >&2
  echo "  (is this a packaged build with asarUnpack: resources/** ?)" >&2
  exit 1
fi

for bin in "${BINS[@]}"; do
  if [ ! -f "$BIN_DIR/$bin" ]; then
    echo "✗ expected nested binary missing: $BIN_DIR/$bin" >&2
    exit 1
  fi
  echo "==> signing nested binary: $bin"
  codesign --force --sign - "$BIN_DIR/$bin"
done

# Self-verification: the whole point is that the nested `multica` must EXECUTE
# (not be SIGKILL'd). exit 137 = a binary is still unsigned / mis-signed.
echo "==> verifying nested 'multica' executes (expect exit 0, NOT 137)"
set +e
"$BIN_DIR/multica" --help >/dev/null 2>&1
rc=$?
set -e
if [ "$rc" -ne 0 ]; then
  echo "✗ nested 'multica --help' exited $rc (137 = Gatekeeper SIGKILL: a nested binary is unsigned). Re-run after confirming the copy finished." >&2
  exit 1
fi

echo "✓ signed $APP + ${#BINS[@]} nested binaries; nested 'multica' runs (exit 0)."
