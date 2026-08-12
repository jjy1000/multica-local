#!/usr/bin/env bash
# ============================================================================
# ship-mac.sh — canonical macOS desktop ship chain, enforced end-to-end.
#
# WHY THIS EXISTS (0.3.66, closes audit P1-1 + P1-3):
# Before this script the entire macOS ship chain lived as prose in CLAUDE.md
# ("Ship chain (canonical order)") plus a handful of standalone scripts that
# NOTHING invoked. Every step depended on the shipper remembering it, and
# skipping any one of them caused a real incident:
#   * skip pre-update snapshot      -> no rollback point      (data-safety risk)
#   * skip `migrate up` pre-bundle  -> SQL error at 1st launch (0.3.20, mig 153)
#   * skip electron-builder --dir   -> stale renderer asar     (0.3.30.1)
#   * skip nested-binary re-sign    -> Gatekeeper SIGKILL, GUI up / backend dead
#                                                          (0.3.62, exit 137)
#   * skip cold-start verify        -> shipped a broken build undetected
# This script runs them in the mandatory order, aborts on the first non-zero
# exit, and — critically — is the first real CALLER of
# scripts/desktop-sign-nested-binaries.sh (which previously had zero callers,
# so "runnable" did not mean "run").
#
# Destructive step: installing into /Applications OVERWRITES the running app.
# The script refuses to do that without an explicit confirmation (interactive
# prompt, or `--yes` for a non-attended run after you have reviewed the build).
#
# Usage:
#   bash scripts/ship-mac.sh                 # full chain; prompts before install
#   bash scripts/ship-mac.sh --build-only    # stop after the asar verification,
#                                            # do NOT touch /Applications
#   bash scripts/ship-mac.sh --yes           # non-interactive: auto-confirm install
#   bash scripts/ship-mac.sh --skip-snapshot # DANGER: skip the data-safety snapshot
#                                            # (only when you just ran one manually)
# ============================================================================
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DESKTOP="$REPO_ROOT/apps/desktop"
BUILT_APP="$DESKTOP/dist/mac-arm64/Multica.app"
INSTALLED_APP="/Applications/Multica.app"
ASAR="$BUILT_APP/Contents/Resources/app.asar"
SNAPSHOT="$HOME/.multica/scripts/pre-update-snapshot.sh"
COLD_START="$HOME/.multica/scripts/verify-desktop-cold-start.sh"
SIGN="$REPO_ROOT/scripts/desktop-sign-nested-binaries.sh"

# Ship metadata (used by step 6b local backup + final summary)
VERSION="$(python3 -c 'import json; print(json.load(open("'"$DESKTOP"'/package.json"))["version"])' 2>/dev/null || echo 'unknown')"
BRANCH="$(git -C "$REPO_ROOT" branch --show-current 2>/dev/null || echo 'unknown')"

BUILD_ONLY=false
ASSUME_YES=false
SKIP_SNAPSHOT=false
for arg in "$@"; do
  case "$arg" in
    --build-only)    BUILD_ONLY=true ;;
    --yes|-y)        ASSUME_YES=true ;;
    --skip-snapshot) SKIP_SNAPSHOT=true ;;
    -h|--help)
      sed -n '2,40p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *) echo "✗ unknown flag: $arg (see --help)" >&2; exit 2 ;;
  esac
done

step() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }
die()  { printf '\n\033[1;31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

cd "$REPO_ROOT"

# --- 1. Data-safety snapshot (mandatory; exit 1 blocks the ship) -------------
if [ "$SKIP_SNAPSHOT" = true ]; then
  step "1/7 pre-update snapshot — SKIPPED (--skip-snapshot). No new rollback point taken."
else
  step "1/7 pre-update data-safety snapshot"
  if [ ! -f "$SNAPSHOT" ]; then
    die "snapshot script not found: $SNAPSHOT (install it or pass --skip-snapshot knowingly)"
  fi
  bash "$SNAPSHOT" || die "pre-update snapshot failed — aborting ship (data-safety invariant)"
fi

# --- 2. Apply pending migrations BEFORE bundling -----------------------------
# Surface SQL errors at build time, not at the user's first cold start (0.3.20).
step "2/7 go run ./cmd/migrate up (surface SQL errors now, not at first launch)"
( cd "$REPO_ROOT/server" && go run ./cmd/migrate up ) || die "migration failed — aborting"

# --- 3. Bundle Go binaries + stage resources ---------------------------------
step "3/7 bundle-cli (Go binaries + migrations + PG/Pythia/manifests -> resources/)"
pnpm --filter @multica/desktop bundle-cli || die "bundle-cli failed — aborting"

# --- 4. Build the renderer (electron-vite; does NOT run electron-builder) ----
step "4/7 electron-vite build (renderer)"
pnpm --filter @multica/desktop build || die "renderer build failed — aborting"

# --- 5. Package to dist/mac-arm64/Multica.app --------------------------------
# MUST run with cwd=apps/desktop: from the repo root electron-builder would
# package the root dist/ and fail with "index.js not found in archive".
# Known deadlock: app-builder-bin@5.0.0-alpha.12 can hang >5min at unpack-electron
# on macOS 27. If it hangs, Ctrl-C and use the manual asar repack fallback in
# CLAUDE.md ("Ship chain fallback"); do NOT set node-linker=hoisted to work around it.
step "5/7 electron-builder --mac --dir (cwd=apps/desktop)"
( cd "$DESKTOP" && pnpm exec electron-builder --mac --dir ) \
  || die "electron-builder failed (if it deadlocked, use the manual asar-repack fallback) — aborting"

# --- 5a. Prove the renderer asar was actually replaced -----------------------
# `pnpm build` does NOT run electron-builder; a skipped/stale packaging step
# leaves the old renderer in the asar (0.3.30.1 shipped this way). rawRequest
# is present in every converted Labs tab, so a zero count means an old asar.
step "5a/7 verify renderer asar was replaced (grep rawRequest)"
[ -f "$ASAR" ] || die "packaged asar not found: $ASAR"
RAW_COUNT="$(grep -c rawRequest "$ASAR" || true)"
if [ "${RAW_COUNT:-0}" -lt 1 ]; then
  die "asar has 0 'rawRequest' occurrences — the renderer was NOT rebuilt into the package. Re-run steps 4-5."
fi
echo "    asar OK: $RAW_COUNT rawRequest occurrence(s)"

if [ "$BUILD_ONLY" = true ]; then
  step "--build-only: stopping before install. Built app: $BUILT_APP"
  echo "    To install manually: cp -R \"$BUILT_APP\" /Applications/ && bash \"$SIGN\" /Applications/Multica.app"
  exit 0
fi

# --- 6. Install into /Applications (DESTRUCTIVE overwrite) -------------------
step "6/7 install into /Applications (overwrites the running app)"
if [ "$ASSUME_YES" != true ]; then
  printf '    This OVERWRITES %s with the freshly built app.\n' "$INSTALLED_APP"
  printf '    A snapshot was taken in step 1. Proceed? [y/N] '
  read -r answer
  case "$answer" in
    y|Y|yes|YES) ;;
    *) die "install not confirmed — aborting (build is intact at $BUILT_APP)" ;;
  esac
fi
cp -R "$BUILT_APP" /Applications/ || die "cp -R into /Applications failed — aborting"

# --- 6a. Re-sign the nested Go binaries (FIRST real caller of the script) ----
# electron-builder --dir only signs the top-level bundle; macOS 27 Gatekeeper
# SIGKILLs the unpacked Go binaries on fork+exec (exit 137) unless each is
# re-signed AFTER the copy. The script self-verifies `multica --help` exits 0.
step "6a/7 re-sign app + nested Go binaries (self-verifying)"
bash "$SIGN" "$INSTALLED_APP" || die "nested-binary signing/verification failed — the app will NOT start its backend"

# --- 6b. Local backup snapshot (.omc/backups/<TS>/<ver>-ship/) ---------------
# Per .omc/backups/README.md (local-project-backup-protocol-2026-08-11): every
# ship captures a manifest + diff + status at /Applications/Multica.app level,
# so a future session can `git apply .omc/backups/<TS>/0.X.Y-ship/diff.patch`
# to recreate the ship state if /Applications gets corrupted or wiped. The
# data-safety snapshot (step 0) is /tmp-only and short-lived; this one lives
# 90 days in .omc/backups/_archive/. --skip-backup available for emergencies.
BACKUP="$REPO_ROOT/scripts/backup.sh"
SKIP_BACKUP="${SKIP_BACKUP:-false}"
if [ "$SKIP_BACKUP" != true ]; then
  if [ ! -x "$BACKUP" ]; then
    die "scripts/backup.sh missing or not executable — refusing to ship without local backup (set SKIP_BACKUP=true to override)"
  fi
  step "6b/7 local backup snapshot (.omc/backups/<TS>/${VERSION}-ship/)"
  bash "$BACKUP" --reason "${VERSION}-ship" --trigger release \
    --notes "ship-mac.sh step 6b; version=$VERSION; branch=$BRANCH; data-safety snapshot at step 1" \
    || die "local backup failed — refusing to ship without snapshot (set SKIP_BACKUP=true to override)"
else
  echo "    SKIP_BACKUP=true: skipping local backup (emergency override)"
fi

# --- 7. Cold-start verification (three-check + row parity) -------------------
step "7/7 cold-start verification"
pkill -f "multica daemon" 2>/dev/null || true
pkill -f "Multica.app/Contents/MacOS/Multica" 2>/dev/null || true
sleep 1
open "$INSTALLED_APP"
if [ -f "$COLD_START" ]; then
  bash "$COLD_START" || die "cold-start verification FAILED — inspect ~/.multica/profiles/<profile>/server.log"
else
  echo "    (verify-desktop-cold-start.sh not found — verify manually: ports 5432/8090 listening, /health ok, row parity)"
fi

step "ship complete: $INSTALLED_APP"
