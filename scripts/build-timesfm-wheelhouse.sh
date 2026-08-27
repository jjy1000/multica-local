#!/usr/bin/env bash
# scripts/build-timesfm-wheelhouse.sh — download the pinned TimesFM
# runtime wheels (requirements-timesfm.txt) into
# apps/desktop/vendor/timesfm-src/wheelhouse/ so the lab's run.sh can
# install STRICTLY offline:
#
#   pip install --no-index --find-links <...>/wheelhouse -r requirements-timesfm.txt
#
# This script is the ONLY networked step in the timesfm supply chain
# and is run manually on a dev mac before packaging — never in-app,
# never in CI without explicit opt-in. The wheelhouse directory is
# gitignored (torch alone is hundreds of MB); it is reproducible from
# requirements-timesfm.txt.
#
# R4 (feasibility report §4): the torch wheel is ARCH-SPECIFIC. This
# script pins macosx_11_0_arm64 + CPython 3.12 (the fork's primary
# darwin/arm64 target). An x64/Intel ship needs its own wheelhouse —
# the script refuses loudly on non-arm64 rather than silently
# producing an unusable bundle (lab degrades to seasonal-naive there).
#
# Usage:
#   bash scripts/build-timesfm-wheelhouse.sh
#
# Exit codes:
#   0  wheelhouse populated
#   1  wrong arch (x64 needs its own wheelhouse build)
#   2  prerequisite missing (python3/pip or requirements file)

set -euo pipefail

if [ "$(uname -s)" != "Darwin" ] || [ "$(uname -m)" != "arm64" ]; then
  cat >&2 <<EOF
[timesfm-wheelhouse] this machine is $(uname -s)/$(uname -m).
The pinned torch wheel is darwin/arm64-only. An x64/Intel ship needs
its OWN wheelhouse (re-run this script on an Intel mac with the
--platform/--python-version flags retargeted) — an arm64 wheelhouse
would silently degrade the lab to seasonal-naive there. Refusing.
EOF
  exit 1
fi

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || true)"
if [ -z "$REPO_ROOT" ]; then
  echo "[timesfm-wheelhouse] not inside a git repository" >&2
  exit 2
fi

REQS="$REPO_ROOT/apps/desktop/vendor/timesfm-src/requirements-timesfm.txt"
WHEELHOUSE="$REPO_ROOT/apps/desktop/vendor/timesfm-src/wheelhouse"
if [ ! -f "$REQS" ]; then
  echo "[timesfm-wheelhouse] requirements file missing: $REQS" >&2
  exit 2
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "[timesfm-wheelhouse] python3 not found" >&2
  exit 2
fi

mkdir -p "$WHEELHOUSE"
# Wipe stale wheels so a pin bump in requirements-timesfm.txt can never
# leave the old version discoverable to --find-links.
rm -f "$WHEELHOUSE"/*.whl 2>/dev/null || true

echo "[timesfm-wheelhouse] downloading pinned wheels (macosx_11_0_arm64, cp312) → $WHEELHOUSE"
echo "[timesfm-wheelhouse] torch dominates the download — expect several hundred MB."
python3 -m pip download \
  --platform macosx_11_0_arm64 \
  --only-binary=:all: \
  --implementation cp \
  --python-version 3.12 \
  -d "$WHEELHOUSE" \
  -r "$REQS"

echo "[timesfm-wheelhouse] done. Contents:"
ls -lh "$WHEELHOUSE"
