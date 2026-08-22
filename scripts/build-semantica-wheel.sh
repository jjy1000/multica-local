#!/usr/bin/env bash
# scripts/build-semantica-wheel.sh — build a PEP 517 wheel from the
# vendored semantica subtree and place it under
# apps/desktop/vendor/semantica-src/builds/ for offline pip install
# (per the plan §2.4.2 L2 verification).
#
# Companion to scripts/sync-semantica-upstream.sh (Stage 3). Also runnable
# standalone for CI:
#   bash scripts/build-semantica-wheel.sh
#
# Exit codes:
#   0  wheel built
#   1  build failed
#   2  prerequisite missing (python -m build / pip)
#   3  output wheel not found (build claimed success but file is missing)

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || true)"
if [ -z "$REPO_ROOT" ]; then
  echo "[wheel] not inside a git repository" >&2
  exit 2
fi
cd "$REPO_ROOT"

SRC_DIR="apps/desktop/vendor/semantica-src"
BUILDS_DIR="$SRC_DIR/builds"

# Version: prefer the version field in the upstream pyproject.toml, fall
# back to the .upstream-version stamp (which is what we stamp in sync).
VERSION="$(python3 -c "import tomllib, sys; print(tomllib.load(open(sys.argv[1], 'rb'))['project']['version'])" "$SRC_DIR/pyproject.toml" 2>/dev/null || true)"
if [ -z "$VERSION" ]; then
  echo "[wheel] could not parse version from $SRC_DIR/pyproject.toml" >&2
  exit 2
fi
echo "[wheel] target version: $VERSION"

# Prereqs
for bin in python3; do
  command -v "$bin" >/dev/null 2>&1 || {
    echo "[wheel] required binary not found: $bin" >&2
    exit 2
  }
done
if ! python3 -c "import build" >/dev/null 2>&1; then
  echo "[wheel] python3 -m 'build' is not importable — install with: pip install build" >&2
  exit 2
fi

# Wipe and rebuild builds/ so stale wheels from a prior version never
# survive a downgrade. (The version-specific filename would otherwise
# leave the old wheel orphaned but discoverable.)
mkdir -p "$BUILDS_DIR"
rm -f "$BUILDS_DIR"/*.whl 2>/dev/null || true

# Build into a tmpdir under builds/ then move only the wheel artifact to
# the final location. This keeps .dist-info / .egg-info / build/ tree
# out of the git index and out of the runtime resources/ mirror.
TMP_OUT="$(mktemp -d -t semantica-build.XXXXXX)"
trap 'rm -rf "$TMP_OUT"' EXIT

echo "[wheel] running python3 -m build --wheel --outdir $TMP_OUT $SRC_DIR"
python3 -m build \
  --wheel \
  --outdir "$TMP_OUT" \
  --no-isolation \
  "$SRC_DIR" 2>&1 | tail -20 || {
  echo "[wheel] build failed" >&2
  exit 1
}

WHEEL_FILE="$(ls "$TMP_OUT"/semantica-*.whl 2>/dev/null | head -1 || true)"
if [ -z "$WHEEL_FILE" ]; then
  echo "[wheel] no .whl artifact in $TMP_OUT" >&2
  exit 3
fi

cp "$WHEEL_FILE" "$BUILDS_DIR/"
echo "[wheel] installed: $BUILDS_DIR/$(basename "$WHEEL_FILE")"
