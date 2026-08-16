#!/usr/bin/env bash
# Semantica Explorer — HTTP-loopback subprocess for Multica.
#
# Starts the Semantica FastAPI server (semantica.explorer) and exposes
# its REST API under /experimental/semantica/* via Multica's generic
# subprocess path + same-origin reverse proxy.
#
# Architecture:
#   desktop manager-factory (subprocess-manager.ts)
#     └── spawns run.sh <port>     ($1 = free loopback port)
#          ├── bash: 5-step python3 probe + uv venv bootstrap
#          └── exec python -m semantica.explorer --graph ... --port $1
#                 ├── /api/health       (probed by subprocess-manager.ts on_ready)
#                 ├── /api/ontology/*   (proxied at /experimental/semantica/api/ontology/*)
#                 ├── /api/decisions/*  (proxied at /experimental/semantica/api/decisions/*)
#                 ├── /api/graph/*      (proxied at /experimental/semantica/api/graph/*)
#                 ├── /api/sparql       (proxied at /experimental/semantica/api/sparql)
#                 ├── /api/provenance/* (proxied at /experimental/semantica/api/provenance/*)
#                 ├── /api/analytics/*  (proxied at /experimental/semantica/api/analytics/*)
#                 ├── /api/temporal/*   (proxied at /experimental/semantica/api/temporal/*)
#                 ├── /api/enrich/*     (proxied at /experimental/semantica/api/enrich/*)
#                 ├── /api/vocabulary/* (proxied at /experimental/semantica/api/vocabulary/*)
#                 ├── /api/export       (proxied at /experimental/semantica/api/export)
#                 └── /api/import       (proxied at /experimental/semantica/api/import)
#
# The Multica reverse proxy (handler/experimental_proxy.go::reverseProxyTo)
# strips Cookie + Authorization but passes X-API-Key through, so the
# Semantica X-API-Key auth header reaches the upstream. For dev mode
# this script sets SEMANTICA_ALLOW_ANONYMOUS=true (single-user fork,
# loopback only — no SSRF exposure). Set SEMANTICA_REQUIRE_AUTH=1 to
# opt into key enforcement.
#
# SEMANTICA_REPO_PATH (env) — required. Path to the Semantica repo
# (must contain `semantica/explorer/__main__.py`). PYTHONPATH is
# exported to this path so `import semantica` works even when the
# user has NOT `pip install -e`'d the package (single-user fork
# dev mode — the probe falls back to finding the local repo on
# PYTHONPATH).

set -eo pipefail

# ------------------------------------------------------------------
# 1. Validate SEMANTICA_REPO_PATH (required, no silent default)
# ------------------------------------------------------------------
if [ -z "${SEMANTICA_REPO_PATH:-}" ]; then
  cat >&2 <<EOF
[semantica] SEMANTICA_REPO_PATH is not set — refusing to start.

Set it to the directory containing the Semantica repo:
    export SEMANTICA_REPO_PATH=/path/to/semantica
    pip install -e "\$SEMANTICA_REPO_PATH"   # one-time, into whichever python the probe picks
EOF
  exit 1
fi

SEMANTICA_REPO="$SEMANTICA_REPO_PATH"

if [ ! -d "$SEMANTICA_REPO/semantica/explorer" ] || [ ! -f "$SEMANTICA_REPO/semantica/explorer/__main__.py" ]; then
  echo "[semantica] $SEMANTICA_REPO does not look like the Semantica repo (missing semantica/explorer/__main__.py)" >&2
  exit 1
fi

# ------------------------------------------------------------------
# 2. Export PYTHONPATH before the probe so `import semantica` works
#    even when the user has NOT `pip install -e`'d the package.
# ------------------------------------------------------------------
export SEMANTICA_REPO_PATH="$SEMANTICA_REPO"
export PYTHONPATH="$SEMANTICA_REPO${PYTHONPATH:+:$PYTHONPATH}"

# ------------------------------------------------------------------
# 3. 5-step python3 probe (mirrors apps/desktop/vendor/pythia-src/run.sh)
# ------------------------------------------------------------------
probe_python() {
  local py="$1"
  [ -x "$py" ] || return 1
  "$py" -c "import fastapi, uvicorn; import semantica" 2>/dev/null
}

PY=""
CANDIDATES=(
  "${HOME}/.multica/semantica-venv/bin/python3"
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

# uv bootstrap (last resort). Installs the explorer extras (fastapi +
# uvicorn) and the local Semantica package via `pip install -e`.
# This is the recommended path for a fresh dev machine.
if [ -z "$PY" ] && command -v uv >/dev/null 2>&1; then
  echo "[semantica] no interpreter had fastapi/uvicorn/semantica; bootstrapping ~/.multica/semantica-venv via uv (one-time)" >&2
  uv venv --python 3.12 "${HOME}/.multica/semantica-venv" >&2
  VIRTUAL_ENV="${HOME}/.multica/semantica-venv" uv pip install fastapi 'uvicorn[standard]' pydantic >&2
  VIRTUAL_ENV="${HOME}/.multica/semantica-venv" uv pip install -e "$SEMANTICA_REPO" >&2
  if probe_python "${HOME}/.multica/semantica-venv/bin/python"; then
    PY="${HOME}/.multica/semantica-venv/bin/python"
  fi
fi

if [ -z "$PY" ]; then
  cat >&2 <<EOF
[semantica] missing Python deps on every available interpreter.

Tried:
$(printf '  - %s\n' "${CANDIDATES[@]}")

Fix (one-time, recommended):
    pip install -e "$SEMANTICA_REPO"          # installs semantica + explorer deps

Fix (clean venv, any platform):
    uv venv ~/.multica/semantica-venv
    VIRTUAL_ENV=~/.multica/semantica-venv uv pip install -e "$SEMANTICA_REPO"
EOF
  exit 127
fi

# ------------------------------------------------------------------
# 4. Resolve graph path. Pre-create an empty ContextGraph if missing
#    so the explorer boots on first launch (GraphSession.from_file
#    fails on a missing file).
# ------------------------------------------------------------------
GRAPH_PATH="${SEMANTICA_KG_PATH:-${MULTICA_RESOURCES_DIR:-${HOME}/.multica}/semantica-graph.json}"

if [ ! -f "$GRAPH_PATH" ]; then
  echo "[semantica] no graph at $GRAPH_PATH — creating an empty ContextGraph" >&2
  "$PY" -c "
from semantica.context import ContextGraph
g = ContextGraph(advanced_analytics=True)
g.save('$GRAPH_PATH')
" 2>/dev/null || {
    echo "[semantica] warning: could not pre-create empty graph; explorer may fail on first boot" >&2
    # Create an empty file so from_file() can at least attempt a load
    : > "$GRAPH_PATH"
  }
fi

export SEMANTICA_KG_PATH="$GRAPH_PATH"
export SEMANTICA_PROVENANCE_DB="${SEMANTICA_PROVENANCE_DB:-$GRAPH_PATH.provenance}"

# ------------------------------------------------------------------
# 5. Auth mode. SEMANTICA_REQUIRE_AUTH=1 → enforce X-API-Key.
#    Default (single-user fork, loopback only): anonymous OK.
# ------------------------------------------------------------------
if [ "${SEMANTICA_REQUIRE_AUTH:-0}" = "1" ]; then
  export SEMANTICA_API_KEY="${SEMANTICA_API_KEY:-$(head -c 32 /dev/urandom | xxd -p -c 32)}"
  echo "[semantica] auth required; X-API-Key written to $GRAPH_PATH.api-key (mode=required)" >&2
  printf '%s' "${SEMANTICA_API_KEY}" > "$GRAPH_PATH.api-key"
  chmod 0600 "$GRAPH_PATH.api-key"
else
  export SEMANTICA_ALLOW_ANONYMOUS=true
fi

# ------------------------------------------------------------------
# 6. cd into the Semantica repo before exec. Required so the
#    Semantica Explorer can find repo-relative config files at
#    startup (cwd-dependent behavior of ContextGraph + GraphSession).
# ------------------------------------------------------------------
cd "$SEMANTICA_REPO"

# ------------------------------------------------------------------
# 7. exec the Semantica explorer. Manager passes the port as $1.
# ------------------------------------------------------------------
exec "$PY" -m semantica.explorer \
  --graph "$GRAPH_PATH" \
  --port "$1" \
  --host 127.0.0.1 \
  --no-browser