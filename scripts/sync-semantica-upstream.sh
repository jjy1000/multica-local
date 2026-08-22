#!/usr/bin/env bash
# scripts/sync-semantica-upstream.sh — pull upstream semantica-agi/semantica
# into the fork subtree, build wheel, optional bundle-cli mirror.
#
# Companion to .omc/plans/semantica-research-and-porting-design.md §2.4.3.
#
# Idempotent. Re-running on a clean tree is a no-op (or a fresh pull of
# the same pin tag). Override the pin via SEMANTICA_PIN env (tag, branch,
# or commit SHA — anything `git subtree add` accepts as the final arg).
#
# Exit codes:
#   0  success
#   1  git / network failure
#   2  prerequisite missing
#   3  wheel build failed
#   4  bundle-cli copy failed

set -euo pipefail

# -------- args / env --------------------------------------------------------
PIN_TAG="${SEMANTICA_PIN:-v0.6.6}"
UPSTREAM_REPO="${SEMANTICA_REPO:-https://github.com/semantica-agi/semantica.git}"
PREFIX="apps/desktop/vendor/semantica-src"
BUNDLE_AFTER_SYNC="${BUNDLE_AFTER_SYNC:-1}"
SKIP_WHEEL="${SKIP_WHEEL:-0}"
SKIP_DIFF="${SKIP_DIFF:-0}"

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || true)"
if [ -z "$REPO_ROOT" ]; then
  echo "[sync] not inside a git repository" >&2
  exit 2
fi
cd "$REPO_ROOT"

VERSION_STAMP="$PREFIX/.upstream-version"

log() { echo "[sync] $*" >&2; }

# -------- prereqs -----------------------------------------------------------
for bin in git python3; do
  command -v "$bin" >/dev/null 2>&1 || {
    echo "[sync] required binary not found: $bin" >&2
    exit 2
  }
done

# -------- Stage 1: first-time import vs subsequent pull --------------------
if [ ! -f "$VERSION_STAMP" ]; then
  log "first-time import from $UPSTREAM_REPO @ $PIN_TAG"
  git subtree add --squash --prefix="$PREFIX" "$UPSTREAM_REPO" "$PIN_TAG"
else
  log "pulling upstream $UPSTREAM_REPO @ $PIN_TAG"
  git subtree pull --squash --prefix="$PREFIX" "$UPSTREAM_REPO" "$PIN_TAG"
fi

# -------- Stage 2: stamp version --------------------------------------------
SQUASHED_SHA="$(git rev-parse HEAD^{tree})"
printf '%s @ %s\n' "$PIN_TAG" "$SQUASHED_SHA" > "$VERSION_STAMP"
log "stamped $VERSION_STAMP"

# -------- Stage 3: build wheel ---------------------------------------------
if [ "$SKIP_WHEEL" = "1" ]; then
  log "skipping wheel build (SKIP_WHEEL=1)"
else
  log "building wheel (delegates to scripts/build-semantica-wheel.sh)"
  bash scripts/build-semantica-wheel.sh || {
    log "wheel build failed (exit $?); continuing but marking the run as failed"
    exit 3
  }
fi

# -------- Stage 4: optional bundle-cli mirror ------------------------------
if [ "$BUNDLE_AFTER_SYNC" = "1" ]; then
  if command -v pnpm >/dev/null 2>&1; then
    log "running pnpm --filter @multica/desktop bundle-cli (BUNDLE_AFTER_SYNC=1)"
    pnpm --filter @multica/desktop bundle-cli || {
      log "bundle-cli failed (exit $?); resources/semantica/ may be stale"
      exit 4
    }
  else
    log "pnpm not on PATH; skipping bundle-cli (resources/semantica/ mirror will be stale until next manual bundle-cli)"
  fi
fi

# -------- Stage 5: print diff summary --------------------------------------
if [ "$SKIP_DIFF" = "1" ]; then
  log "skipping upstream diff (SKIP_DIFF=1)"
else
  log "running check-semantica-upstream.sh for diff summary"
  if bash scripts/check-semantica-upstream.sh --quiet 2>&1; then
    log "no upstream drift detected"
  else
    log "upstream drift detected — see output above (non-fatal)"
  fi
fi

log "done"
