#!/usr/bin/env bash
# Semantica Explorer — HTTP-loopback subprocess for Multica.
#
# Architecture:
#   desktop manager-factory (subprocess-manager.ts)
#     └── spawns run.sh <port>     ($1 = free loopback port)
#          ├── bash: 5-step python3 probe + uv venv bootstrap
#          └── exec python -m semantica.explorer --graph ... --port $1
#
# The Multica reverse proxy (handler/experimental_proxy.go::reverseProxyTo)
# strips Cookie + Authorization and unconditionally replaces any
# caller-supplied X-API-Key with the value the desktop main process
# set via upstreamRegister (R4 P1-1 mitigation). Default mode is
# anonymous (SEMANTICA_ALLOW_ANONYMOUS=true); set
# SEMANTICA_REQUIRE_AUTH=1 to enforce the shared key.
#
# 0.5.29 P0-2: SEMANTICA_WORKSPACE_ID env is required. The graph.json
# path is per-workspace (~/.multica/workspaces/<wsId>/semantica-graph.json);
# the legacy global path is `cp`'d into the new path on first boot
# (one-shot migration), then left on disk as a backup.
#
# 0.5.29 P1-1: SEMANTICA_API_KEY env is required (no silent xxd
# fallback) — the desktop main process owns the key and threads it
# via env, then re-forwards it to the server via upstreamRegister IPC.
# The pre-0.5.29 file at $GRAPH_PATH.api-key is gone; that file was
# reachable by any user-plugin `python3 -I` child (F-013 class).

set -eo pipefail

# ------------------------------------------------------------------
# 1. Validate SEMANTICA_REPO_PATH (required, no silent default).
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
  echo "[semantica] $SEMANTICA_REPO does not look like the semantica repo (missing semantica/explorer/__main__.py)" >&2
  exit 1
fi

# ------------------------------------------------------------------
# 2. Validate SEMANTICA_WORKSPACE_ID (0.5.29 P0-2). UUID-regex
#    (8-4-4-4-12 lowercase hex). subprocess-manager.ts already
#    validates upstream; this is defense-in-depth so a smuggled
#    env can never reach the Python heredoc as raw interpolation.
# ------------------------------------------------------------------
if [ -z "${SEMANTICA_WORKSPACE_ID:-}" ]; then
  echo "[semantica] SEMANTICA_WORKSPACE_ID is not set — refusing to start." >&2
  exit 1
fi
if ! printf '%s' "$SEMANTICA_WORKSPACE_ID" | grep -Eq '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'; then
  echo "[semantica] SEMANTICA_WORKSPACE_ID='$SEMANTICA_WORKSPACE_ID' is not a valid UUID; refusing to start." >&2
  exit 1
fi
WS_ID="$SEMANTICA_WORKSPACE_ID"

# 0.5.29 P1-1: SEMANTICA_API_KEY is the per-launch shared secret. The
# desktop main process generates it (randomBytes 32B hex) and
# forwards it to the server via upstreamRegister IPC. The server's
# reverse-proxy Director unconditionally sets X-API-Key to this value.
# We refuse to fall back to a self-generated xxd key here so the
# per-launch file at $GRAPH_PATH.api-key can be eliminated (F-013).
if [ -z "${SEMANTICA_API_KEY:-}" ]; then
  echo "[semantica] SEMANTICA_API_KEY is not set — refusing to start." >&2
  echo "[semantica] (The desktop main process owns the per-launch X-API-Key; spawn comes from subprocess-manager.ts.)" >&2
  exit 1
fi

# ------------------------------------------------------------------
# 3. Export PYTHONPATH before the probe so `import semantica` works
#    even when the user has NOT `pip install -e`'d the package.
# ------------------------------------------------------------------
export SEMANTICA_REPO_PATH="$SEMANTICA_REPO"
export SEMANTICA_WORKSPACE_ID="$WS_ID"
export PYTHONPATH="$SEMANTICA_REPO${PYTHONPATH:+:$PYTHONPATH}"

# ------------------------------------------------------------------
# 4. 5-step python3 probe (mirrors apps/desktop/vendor/pythia-src/run.sh)
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
# 5. Resolve per-workspace graph path (0.5.29 P0-2):
#      ~/.multica/workspaces/<wsId>/semantica-graph.json
#    Pre-create the parent dir (per-workspace dirs may not exist
#    yet for a fresh workspace). Pre-create the file with an empty
#    ContextGraph if missing — Semantica's GraphSession.from_file
#    fails on a missing file.
#
#    Legacy `cp` migration: when upgrading from 0.5.28 with the
#    legacy global path (~/.multica/semantica-graph.json + sibling
#    .provenance), we COPY (not mv, not ln) into the new per-workspace
#    path. The legacy file stays on disk as a backup; one-shot only.
# ------------------------------------------------------------------
GRAPH_PATH="${SEMANTICA_KG_PATH:-${HOME}/.multica/workspaces/${WS_ID}/semantica-graph.json}"
PROVENANCE_PATH="${SEMANTICA_PROVENANCE_DB:-$GRAPH_PATH.provenance}"

GRAPH_DIR="$(dirname "$GRAPH_PATH")"
mkdir -p "$GRAPH_DIR"

LEGACY_GRAPH="${HOME}/.multica/semantica-graph.json"
LEGACY_PROV="${HOME}/.multica/semantica-graph.json.provenance"
if [ ! -f "$GRAPH_PATH" ] && [ -f "$LEGACY_GRAPH" ]; then
  cp -p "$LEGACY_GRAPH" "$GRAPH_PATH" && {
    echo "[semantica] legacy migration: $LEGACY_GRAPH -> $GRAPH_PATH" >&2
  }
fi
if [ ! -f "$PROVENANCE_PATH" ] && [ -f "$LEGACY_PROV" ]; then
  cp -p "$LEGACY_PROV" "$PROVENANCE_PATH" && {
    echo "[semantica] legacy migration: $LEGACY_PROV -> $PROVENANCE_PATH" >&2
  }
fi

if [ ! -f "$GRAPH_PATH" ]; then
  echo "[semantica] no graph at $GRAPH_PATH — creating an empty ContextGraph" >&2
  # Pass wsId as an env var read by Python — NOT string-interpolated
  # into the heredoc. That is the R4 P0-2c RCE mitigation: wsId is
  # already a strict UUID, but passing it via env keeps the
  # injection surface zero even if the regex is ever loosened.
  WS_ID_FOR_PY="$WS_ID" "$PY" -c '
import os
from semantica.context import ContextGraph
ws_id = os.environ["WS_ID_FOR_PY"]
g = ContextGraph(advanced_analytics=True)
g.save(os.path.join(os.path.expanduser("~"), ".multica", "workspaces", ws_id, "semantica-graph.json"))
' 2>/dev/null || {
    echo "[semantica] warning: could not pre-create empty graph; explorer may fail on first boot" >&2
    : > "$GRAPH_PATH"
  }
fi

export SEMANTICA_KG_PATH="$GRAPH_PATH"
export SEMANTICA_PROVENANCE_DB="$PROVENANCE_PATH"

# ------------------------------------------------------------------
# 6. Auth mode. SEMANTICA_REQUIRE_AUTH=1 → enforce X-API-Key via
#    SEMANTICA_ALLOW_ANONYMOUS toggle (the upstream Semantica
#    explorer honors the env var). Default (single-user fork,
#    loopback only): anonymous OK; key is still set on every
#    Director call so an attacker cannot pivot by setting it
#    themselves.
# ------------------------------------------------------------------
if [ "${SEMANTICA_REQUIRE_AUTH:-0}" = "1" ]; then
  export SEMANTICA_REQUIRE_AUTH=1
else
  export SEMANTICA_ALLOW_ANONYMOUS=true
fi

# ------------------------------------------------------------------
# 7. cd into the Semantica repo before exec. Required so the
#    Semantica Explorer can find repo-relative config files at
#    startup (cwd-dependent behavior of ContextGraph + GraphSession).
# ------------------------------------------------------------------
cd "$SEMANTICA_REPO"

# ------------------------------------------------------------------
# 8. exec the Semantica explorer. Manager passes the port as $1.
# ------------------------------------------------------------------
exec "$PY" -m semantica.explorer \
  --graph "$GRAPH_PATH" \
  --port "$1" \
  --host 127.0.0.1 \
  --no-browser