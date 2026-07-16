#!/usr/bin/env bash
# Pythia starter source-of-record — bundle-cli.mjs copies this to
# apps/desktop/resources/pythia/run.sh on every bundle. Edit THIS file
# (NOT resources/pythia/run.sh) so the change survives a re-bundle.
#
# See bundle-cli.mjs `wrapper` template for the full policy.
#
# 0.3.30: zero-config python discovery. The Pythia engine is now 3.14
# compatible (verified 2026-07-16 on brew CPython 3.14.1 +
# fastapi/uvicorn/etc). We probe five candidate interpreters in order
# and accept the first one whose `import` probe succeeds:
#
#   1. ~/.multica/pythia-venv/bin/python3     (user-provisioned venv)
#   2. /opt/homebrew/bin/python3               (Apple Silicon homebrew)
#   3. /usr/local/bin/python3                  (Intel homebrew)
#   4. $(command -v python3)                   (PATH winner)
#   5. Auto-create ~/.multica/pythia-venv via uv as a last resort.
#      uv is bundled with the desktop via its postinstall script, so
#      this branch is non-interactive in practice.
#
# If step 1-4 all fail (no deps on any system python), step 5 fires
# `uv venv --python 3.12 ... && uv pip install -r requirements.txt`
# and uses the resulting venv. Brew users with stock Python 3.14
# land on step 2 with a clear "missing deps — here is the one-liner"
# message instead.
set -eo pipefail
HERE="$PWD"
# Resolve HERE more robustly: the desktop main process may spawn us
# from a different cwd in some ManagerFactory paths. If the engine
# dir is not right next to us, walk up to find it.
if [ ! -d "$HERE/engine" ]; then
  for parent in "$HERE/.." "$HERE/../.."; do
    if [ -d "$parent/engine" ] && [ -f "$parent/engine/server.py" ]; then
      HERE="$(cd "$parent" && pwd)"
      break
    fi
  done
fi
export PYTHONPATH="$HERE/engine${PYTHONPATH:+:$PYTHONPATH}"

probe_python() {
  local py="$1"
  [ -x "$py" ] || return 1
  "$py" -c "import fastapi, uvicorn, httpx, dotenv, pydantic" 2>/dev/null
}

# Build candidate list (ordered). We do NOT pre-set PY here — we want
# the for-loop below to be the SOLE authority on which interpreter to
# use, so a partial candidate list (e.g. only system python3 with no
# deps) cleanly falls through to the bootstrap branch.
PY=""
CANDIDATES=(
  "${HOME}/.multica/pythia-venv/bin/python3"
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
  # Last-resort bootstrap: if uv is available, create the venv and try
  # again. We prefer Python 3.12 because it is the lowest version
  # where every engine import works without a 3.14 syntax pass; the
  # 3.14 path is supported via the candidate list above when the user
  # has already provisioned deps against a brew python.
  if command -v uv >/dev/null 2>&1; then
    echo "[pythia] no interpreter had the bundled deps; bootstrapping ~/.multica/pythia-venv via uv (one-time)" >&2
    uv venv --python 3.12 "${HOME}/.multica/pythia-venv" >&2
    VIRTUAL_ENV="${HOME}/.multica/pythia-venv" uv pip install -r "$HERE/requirements.txt" >&2
    if probe_python "${HOME}/.multica/pythia-venv/bin/python3"; then
      PY="${HOME}/.multica/pythia-venv/bin/python3"
    fi
  fi
fi

if [ -z "$PY" ]; then
  cat >&2 <<EOF
[pythia] missing Python deps on every available interpreter.

Tried:
$(printf '  - %s\n' "${CANDIDATES[@]}")

Fix (brew python 3.14):
    /opt/homebrew/bin/python3 -m pip install --user --break-system-packages -r '$HERE/requirements.txt'

Fix (clean venv, any platform):
    uv venv ~/.multica/pythia-venv
    VIRTUAL_ENV=~/.multica/pythia-venv uv pip install -r '$HERE/requirements.txt'
EOF
  exit 127
fi

exec "$PY" -m uvicorn engine.server:app --host 127.0.0.1 --port "$1"
