#!/usr/bin/env bash
# ==========================================================================
# Build the vendored Semantica wheel from source.
#
# Referenced by:
#   - apps/desktop/resources/semantica/run.sh   (wheel is its install source)
#   - apps/desktop/scripts/bundle-cli.mjs       (warns to run THIS when
#                                                semantica-src/builds/ is
#                                                empty instead of aborting)
#
# Source of record is apps/desktop/vendor/semantica-src/ (the same rule as
# pythia-src). Output lands next to the sources in semantica-src/builds/;
# bundle-cli mirrors it into resources/semantica/builds/ at packaging time,
# so there is no need to touch resources/ here.
#
# Usage:
#   bash apps/desktop/scripts/build-semantica-wheel.sh
# ==========================================================================
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC_DIR="$REPO_ROOT/apps/desktop/vendor/semantica-src"

if [ ! -f "$SRC_DIR/pyproject.toml" ]; then
  echo "[build-semantica-wheel] ERROR: $SRC_DIR/pyproject.toml not found." >&2
  echo "[build-semantica-wheel] The vendored Semantica subtree is missing; restore it before building." >&2
  exit 1
fi

mkdir -p "$SRC_DIR/builds"

echo "[build-semantica-wheel] building wheel from $SRC_DIR ..."
python3 -m pip wheel --no-deps --wheel-dir "$SRC_DIR/builds" "$SRC_DIR"

echo "[build-semantica-wheel] done:"
ls -1 "$SRC_DIR"/builds/*.whl
echo "[build-semantica-wheel] next: re-run bundle-cli so the new wheel is mirrored into resources/semantica/builds/."
