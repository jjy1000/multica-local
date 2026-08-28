#!/usr/bin/env bash
# TimesFM lab starter source-of-record — bundle-cli.mjs copies this to
# apps/desktop/resources/timesfm/run.sh on every bundle. Edit THIS file
# (NOT resources/timesfm/run.sh) so the change survives a re-bundle.
#
# Mirrors the pythia-src/run.sh five-way interpreter probe (0.3.30
# idiom), with ONE hard fork difference: the last-resort venv bootstrap
# installs STRICTLY offline from the bundled wheelhouse —
#
#     pip install --no-index --find-links "$HERE/wheelhouse" \
#         -r requirements-timesfm.txt
#
# — never PyPI. torch + pandas make a networked install both slow and a
# fork-law violation (no in-app network). If the wheelhouse is missing
# the bootstrap fails loudly with the rebuild one-liner.
#
# Boot chain after interpreter resolution:
#     exec python -m uvicorn run_loopback:app --host 127.0.0.1 --port $1
#
# run_loopback.py lives next to this script and imports the vendored
# ./timesfm package (both are put on PYTHONPATH below). Weights, if the
# user seeded them, live at ~/.multica/models/timesfm/model.safetensors;
# the Python process enforces HF_HUB_OFFLINE=1 itself.
set -eo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# Resolve HERE more robustly: the desktop main process may spawn us
# from a different cwd in some ManagerFactory paths. If the vendored
# package is not right next to us, walk up to find it.
if [ ! -d "$HERE/timesfm" ]; then
  for parent in "$HERE/.." "$HERE/../.."; do
    if [ -d "$parent/timesfm" ] && [ -f "$parent/timesfm/__init__.py" ]; then
      HERE="$(cd "$parent" && pwd)"
      break
    fi
  done
fi
export PYTHONPATH="$HERE${PYTHONPATH:+:$PYTHONPATH}"

probe_python() {
  local py="$1"
  [ -x "$py" ] || return 1
  "$py" -c "import fastapi, uvicorn, torch, numpy, safetensors, huggingface_hub, pandas" 2>/dev/null
}

# Build candidate list (ordered). Same contract as pythia: the loop
# below is the SOLE authority on which interpreter wins.
PY=""
CANDIDATES=(
  "${HOME}/.multica/timesfm-venv/bin/python3"
  "/opt/homebrew/bin/python3"
  "/usr/local/bin/python3"
)
if [ -n "${PATH_PY:-}" ]; then
  CANDIDATES+=("$PATH_PY")
fi
if command -v python3 >/dev/null 2>&1; then
  PATH_PY="$(command -v python3)"
  CANDIDATES+=("$PATH_PY")
fi

for c in "${CANDIDATES[@]}"; do
  if probe_python "$c"; then
    PY="$c"
    break
  fi
done

if [ -z "$PY" ]; then
  # Last-resort bootstrap: reuse ~/.multica/timesfm-venv across launches
  # so the heavy torch install is amortised. STRICTLY offline — the
  # wheelhouse must be staged next to this script (resources/timesfm/
  # wheelhouse in the packaged app; apps/desktop/vendor/timesfm-src/
  # wheelhouse in dev after running scripts/build-timesfm-wheelhouse.sh).
  WHEELHOUSE=""
  for candidate in "$HERE/wheelhouse" "$HERE/../../vendor/timesfm-src/wheelhouse"; do
    if [ -d "$candidate" ]; then
      WHEELHOUSE="$(cd "$candidate" && pwd)"
      break
    fi
  done
  if [ -z "$WHEELHOUSE" ]; then
    cat >&2 <<EOF
[timesfm] no interpreter had the pinned deps AND no wheelhouse found.

Looked at:
  - $HERE/wheelhouse
  - $HERE/../../vendor/timesfm-src/wheelhouse

Fix (one-time, on a networked dev mac):
    bash scripts/build-timesfm-wheelhouse.sh
then re-enable the TimesFM lab.
EOF
    exit 127
  fi
  if command -v uv >/dev/null 2>&1; then
    echo "[timesfm] bootstrapping ~/.multica/timesfm-venv via uv (offline wheelhouse)" >&2
    uv venv --python 3.12 "${HOME}/.multica/timesfm-venv" >&2
  elif command -v python3 >/dev/null 2>&1; then
    echo "[timesfm] bootstrapping ~/.multica/timesfm-venv via python3 -m venv (offline wheelhouse)" >&2
    python3 -m venv "${HOME}/.multica/timesfm-venv" >&2
  else
    echo "[timesfm] neither uv nor python3 available; cannot bootstrap venv" >&2
    exit 127
  fi
  "${HOME}/.multica/timesfm-venv/bin/pip" install \
    --no-index --find-links "$WHEELHOUSE" \
    -r "$HERE/requirements-timesfm.txt" >&2 || {
    echo "[timesfm] offline wheelhouse install failed — wheelhouse may not match requirements-timesfm.txt" >&2
    exit 127
  }
  if probe_python "${HOME}/.multica/timesfm-venv/bin/python3"; then
    PY="${HOME}/.multica/timesfm-venv/bin/python3"
  fi
fi

if [ -z "$PY" ]; then
  cat >&2 <<EOF
[timesfm] missing Python deps on every available interpreter.

Tried:
$(printf '  - %s\n' "${CANDIDATES[@]}")

Fix (clean offline venv):
    bash scripts/build-timesfm-wheelhouse.sh      # stage wheelhouse
    uv venv ~/.multica/timesfm-venv --python 3.12
    ~/.multica/timesfm-venv/bin/pip install --no-index \\
        --find-links '<wheelhouse>' -r '$HERE/requirements-timesfm.txt'
EOF
  exit 127
fi

exec "$PY" -m uvicorn run_loopback:app --host 127.0.0.1 --port "$1"
