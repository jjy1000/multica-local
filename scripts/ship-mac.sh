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

# Ship metadata (used by step 6b local backup; sourced once at script top so the
# backup reason slug is stable across all step invocations)
VERSION="$(sed -n 's/.*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "$DESKTOP/package.json" | head -1)"
BRANCH="$(git -C "$REPO_ROOT" rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)"

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

# --- 4a. Verify the renderer output before packaging ----------------------------
# A stale `apps/desktop/out/` from a previous broken build can survive if
# vite's incremental cache is dirty, and electron-builder then packages the
# corrupted index.html into the asar. The visible symptom is the Electron
# main process exits silently within ~200ms (HTML parser fails on
# `<!doctype>` not being the first byte, OR an inlined JS module
# prepended to the head). Caught here before packaging instead of at
# cold-start verify (memory 0.5.30 — 0.5.29 ship crashed silently because
# this check was missing). Three invariants: file exists, first byte is
# '<', reasonable size. rawRequest still catches stale renderer.
step "4a/7 verify renderer index.html is well-formed (first byte = '<', size in [200, 5000])"
INDEX_HTML="$DESKTOP/out/renderer/index.html"
[ -f "$INDEX_HTML" ] || die "renderer output missing: $INDEX_HTML — electron-vite build did not produce index.html"
INDEX_SIZE=$(wc -c < "$INDEX_HTML" | tr -d ' ')
INDEX_FIRST_BYTE=$(head -c 1 "$INDEX_HTML" | od -An -c | tr -d ' ' | head -c 1)
if [ "$INDEX_FIRST_BYTE" != '<' ]; then
  echo "    FAIL: index.html first byte is '$INDEX_FIRST_BYTE' (0x$(head -c 1 "$INDEX_HTML" | od -An -tx1 | tr -d ' ')) — expected '<' (start of '<!doctype html>')"
  echo "    First 200 bytes of the broken file:"
  head -c 200 "$INDEX_HTML" | sed 's/^/      /'
  die "renderer index.html is corrupted — Vite likely inlined an asset (history: 0.5.30, after Semantica Round 7+ + MUL-6254). Wipe apps/desktop/out/ and re-run step 4."
fi
if [ "$INDEX_SIZE" -lt 200 ] || [ "$INDEX_SIZE" -gt 5000 ]; then
  die "renderer index.html size $INDEX_SIZE bytes is out of [200, 5000] range — Vite build produced an unexpected artifact. Wipe apps/desktop/out/ and re-run step 4."
fi
echo "    index.html OK: $INDEX_SIZE bytes, starts with '<'"

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

# --- 5b. Prove the asar's DATA REGION is not corrupted -----------------------
# The 0.5.94-ship-1 blocker: the asar's header (file table) was written fine
# but every data entry sat ~5 bytes off — each file extracted as "missing its
# first bytes + tail of its neighbour", so package.json failed JSON.parse and
# Electron main exited(1) silently ~150ms after launch, with nothing on
# stderr. grep gates pass on shifted bytes, so this needs a CONTENT check:
# read two canaries straight out of the archive (dependency-free asar reader)
# and byte-compare them against the build output. Run BEFORE install.
step "5b/7 verify asar data region integrity (byte-compare canaries)"
node - "$ASAR" "$DESKTOP" << 'NODE_EOF' || die "packaged asar is CORRUPT: extracted canaries differ from the build output (asar data-region offset — 0.5.94-ship-1 lesson). Re-run steps 4-5 with no concurrent heavy I/O."
const fs = require("fs");
const [asarPath, desktopDir] = process.argv.slice(2);
const fd = fs.openSync(asarPath, "r");
const sizeBuf = Buffer.alloc(16);
fs.readSync(fd, sizeBuf, 0, 16, 0);
const headerSize = sizeBuf.readUInt32LE(4);
const jsonSize = sizeBuf.readUInt32LE(12);
const jsonBuf = Buffer.alloc(jsonSize);
fs.readSync(fd, jsonBuf, 0, jsonSize, 16);
const table = JSON.parse(jsonBuf.toString("utf8"));
const dataStart = 8 + headerSize;
function extract(nodePath) {
  let node = table;
  for (const part of nodePath.split("/")) node = node.files[part];
  const buf = Buffer.alloc(node.size);
  fs.readSync(fd, buf, 0, node.size, dataStart + Number(node.offset));
  return buf;
}
const canaries = [
  // byte-compare: a real code file must be identical to the build output
  ["out/main/index.js", "raw"],
  // JSON-validate only: electron-builder REWRITES asar package.json (strips
  // devDeps/scripts), so it legitimately differs from the source file; the
  // corruption signature is invalid/truncated JSON, not a size delta
  ["package.json", "json"],
];
for (const [asarFile, mode] of canaries) {
  const packaged = extract(asarFile);
  if (mode === "json") {
    try {
      JSON.parse(packaged.toString("utf8"));
    } catch (err) {
      console.error(`    canary FAIL: packaged ${asarFile} is not valid JSON (${packaged.length}B) — asar data region is offset/corrupt`);
      fs.closeSync(fd);
      process.exit(1);
    }
    continue;
  }
  const source = fs.readFileSync(`${desktopDir}/${asarFile}`);
  if (!packaged.equals(source)) {
    console.error(`    canary MISMATCH: ${asarFile} (${packaged.length}B packaged vs ${source.length}B source)`);
    fs.closeSync(fd);
    process.exit(1);
  }
}
fs.closeSync(fd);
console.log("    asar data region OK (main/index.js byte-identical, package.json valid)");
NODE_EOF

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
# ship captures manifest + diff + status at /Applications level, so a future
# session can `git apply .omc/backups/<TS>/0.X.Y-ship/diff.patch` to recreate
# the ship state if /Applications gets corrupted or wiped. The data-safety
# snapshot (step 1) is /tmp-only and short-lived; this one lives 90 days in
# .omc/backups/_archive/. SKIP_BACKUP=true bypasses (emergency only).
BACKUP="$REPO_ROOT/scripts/backup.sh"
SKIP_BACKUP="${SKIP_BACKUP:-false}"
if [ "$SKIP_BACKUP" != true ]; then
  if [ ! -x "$BACKUP" ]; then
    die "scripts/backup.sh missing or not executable — refusing to ship without local backup (set SKIP_BACKUP=true to override)"
  fi
  step "6b/7 local backup snapshot (.omc/backups/<TS>/${VERSION}-ship/)"
  bash "$BACKUP" --reason "${VERSION}-ship" --trigger release \
    || die "local backup failed — refusing to ship without snapshot (set SKIP_BACKUP=true to override)"
else
  echo "    SKIP_BACKUP=true: skipping local backup (emergency override)"
fi

# --- 7. Cold-start verification (FATAL — 0.5.99 lesson) ---------------------
# Belt-and-suspenders: the verify script must run AND succeed. After it
# returns 0, we independently re-confirm a server PID is bound on :8090
# and /health responds "ok". This catches two failure modes:
#   (1) verify script bug that exits 0 on a real failure (0.5.98: server
#       ReferenceError caused :8090 to never bind, but the install was
#       treated as "successful" because verify was non-fatal at the
#       outer level — the new die-on-missing + post-verify sanity here
#       close that gap).
#   (2) verify script missing entirely — refuse to ship silently.
# Why this is in ship-mac.sh (not just the verify script): the verify
# script is in $HOME/.multica/scripts/, outside the fork repo. Any bug
# there escapes our test surface; the post-verify checks below live in
# the repo and are reviewed in this script's diff.
step "7/7 cold-start verification (FATAL — belt-and-suspenders)"
pkill -f "multica daemon" 2>/dev/null || true
pkill -f "Multica.app/Contents/MacOS/Multica" 2>/dev/null || true
sleep 1
open "$INSTALLED_APP"
[ -f "$COLD_START" ] \
  || die "verify-desktop-cold-start.sh not found at $COLD_START — refusing to ship without cold-start verification (0.5.99 lesson)"
bash "$COLD_START" \
  || die "cold-start verification FAILED — inspect ~/.multica/profiles/<profile>/server.log"

# Independent post-verify sanity checks. Even if the verify script reports
# PASS, re-check the two conditions that define "the app actually works".
SERVER_PID=$(lsof -nP -iTCP:8090 -sTCP:LISTEN -t 2>/dev/null | head -1 || true)
[ -n "$SERVER_PID" ] \
  || die "cold-start verify returned 0 but no server PID is bound on :8090 — verify script bug, refusing to ship"
HEALTH=$(curl -s --max-time 3 http://localhost:8090/health 2>/dev/null || echo "")
echo "$HEALTH" | grep -q '"status":"ok"' \
  || die "cold-start verify returned 0 but /health is not ok: $HEALTH — refusing to ship"
echo "[verify] Belt-and-suspenders sanity OK: server PID $SERVER_PID on :8090, /health=ok"

step "ship complete: $INSTALLED_APP"
