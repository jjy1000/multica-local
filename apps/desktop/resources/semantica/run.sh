#!/usr/bin/env bash
# Semantica Explorer — HTTP-loopback subprocess for Multica.
#
# Architecture:
#   desktop manager-factory (subprocess-manager.ts)
#     └── spawns run.sh <port>     ($1 = free loopback port)
#          ├── bash: locate prebuilt wheel in ./builds/ or ../../semantica-src/builds/
#          ├── bash: pip install --no-index wheel into ~/.multica/semantica-venv/
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
#
# 0.5.53 P1 (Phase 1 / 0.5.52 plan §2.3): dropped the
# SEMANTICA_REPO_PATH + pip install -e path. The wheel is prebuilt
# by scripts/build-semantica-wheel.sh from the vendored subtree at
# apps/desktop/vendor/semantica-src/. The same wheel ships in
# apps/desktop/resources/semantica/builds/ via bundle-cli (mirrors
# the pythia-src pattern). One minor version of compat-warn is
# retained: setting SEMANTICA_REPO_PATH still works (the env var
# is ignored after a one-line deprecation notice). 0.5.54 will
# remove the warning entirely.

set -eo pipefail

# ------------------------------------------------------------------
# 0. P1 compat notice — SEMANTICA_REPO_PATH is deprecated. The wheel
#    path makes it redundant. Ignored after a single stderr line.
#    Removed in 0.5.54 (P2 plan).
# ------------------------------------------------------------------
if [ -n "${SEMANTICA_REPO_PATH:-}" ]; then
  echo "[semantica] WARNING: SEMANTICA_REPO_PATH is deprecated as of 0.5.53 (Phase 1); the wheel path is now the source of truth. Remove this env var from your shell profile. Will be removed in 0.5.54." >&2
fi

# ------------------------------------------------------------------
# 1. Validate SEMANTICA_WORKSPACE_ID (0.5.29 P0-2). UUID-regex
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

# ------------------------------------------------------------------
# 2. SEMANTICA_API_KEY is the per-launch shared secret. The
#    desktop main process generates it (randomBytes 32B hex) and
#    forwards it to the server via upstreamRegister IPC. The
#    server's reverse-proxy Director unconditionally sets X-API-Key
#    to this value. We refuse to fall back to a self-generated xxd
#    key here so the per-launch file at $GRAPH_PATH.api-key can be
#    eliminated (F-013).
# ------------------------------------------------------------------
if [ -z "${SEMANTICA_API_KEY:-}" ]; then
  echo "[semantica] SEMANTICA_API_KEY is not set — refusing to start." >&2
  echo "[semantica] (The desktop main process owns the per-launch X-API-Key; spawn comes from subprocess-manager.ts.)" >&2
  exit 1
fi

# ------------------------------------------------------------------
# 3. Locate the prebuilt wheel. Resolution order:
#      a. ./builds/                — packaged (resources/semantica/) + dev after bundle-cli
#      b. ../../semantica-src/builds/ — dev before bundle-cli
#    bundle-cli (apps/desktop/scripts/bundle-cli.mjs) mirrors the
#    semantica-src/builds/ tree into resources/semantica/builds/
#    every run, mirroring the pythia-src pattern.
# ------------------------------------------------------------------
HERE="$(cd "$(dirname "$0")" && pwd)"
BUILDS_DIR=""
for candidate in "$HERE/builds" "$HERE/../../semantica-src/builds"; do
  if [ -d "$candidate" ]; then
    BUILDS_DIR="$(cd "$candidate" && pwd)"
    break
  fi
done
if [ -z "$BUILDS_DIR" ]; then
  cat >&2 <<EOF
[semantica] no prebuilt wheel found.

Looked at:
  - $HERE/builds
  - $HERE/../../semantica-src/builds

To build the wheel:
    bash scripts/sync-semantica-upstream.sh        # rebuild + run monitor
  or:
    bash scripts/build-semantica-wheel.sh          # wheel only
EOF
  exit 1
fi

WHEEL_FILE="$(ls "$BUILDS_DIR"/semantica-*.whl 2>/dev/null | head -1 || true)"
if [ -z "$WHEEL_FILE" ]; then
  echo "[semantica] no .whl file in $BUILDS_DIR; run bash scripts/build-semantica-wheel.sh" >&2
  exit 1
fi

# ------------------------------------------------------------------
# 4. Resolve / install the runtime venv. We reuse
#    ~/.multica/semantica-venv/ across launches so the wheel install
#    is amortised. The venv was originally bootstrapped by the
#    pre-P1 run.sh's uv path; we keep that path compatible by
#    honouring an existing venv even if its python is <3.12 (the
#    upstream pyproject requires >=3.8, so this is safe).
# ------------------------------------------------------------------
VENV="${SEMANTICA_VENV:-${HOME}/.multica/semantica-venv}"
VENV_PY="$VENV/bin/python3"
if [ ! -x "$VENV_PY" ]; then
  echo "[semantica] venv missing at $VENV; bootstrapping" >&2
  if command -v uv >/dev/null 2>&1; then
    uv venv --python 3.12 "$VENV" >&2 || {
      echo "[semantica] uv venv bootstrap failed" >&2; exit 1; }
  elif command -v python3 >/dev/null 2>&1; then
    python3 -m venv "$VENV" >&2 || {
      echo "[semantica] python3 -m venv bootstrap failed" >&2; exit 1; }
  else
    echo "[semantica] neither uv nor python3 available; cannot bootstrap venv" >&2
    exit 1
  fi
fi

# Install (or re-install) the wheel. pip is idempotent — it skips when
# the same version is already installed. We don't pin an exact version
# here so a fresh sync (e.g. v0.6.7) auto-upgrades on next boot.
echo "[semantica] installing $WHEEL_FILE into $VENV" >&2
"$VENV/bin/pip" install --quiet --no-index --find-links "$BUILDS_DIR" "$(basename "$WHEEL_FILE")" >&2 \
  || "$VENV/bin/pip" install --quiet --find-links "$BUILDS_DIR" "$(basename "$WHEEL_FILE")" >&2 \
  || {
    echo "[semantica] wheel install failed; venv may be stale" >&2
    exit 1
  }

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
  cp -p "$LEGACY_PROV" "$PROVENANCE_PATH"
  echo "[semantica] legacy migration: $LEGACY_PROV -> $PROVENANCE_PATH" >&2
fi

if [ ! -f "$GRAPH_PATH" ]; then
  echo "[semantica] no graph at $GRAPH_PATH — creating an empty ContextGraph" >&2
  # Pass wsId as an env var read by Python — NOT string-interpolated
  # into the heredoc. That is the R4 P0-2c RCE mitigation: wsId is
  # already a strict UUID, but passing it via env keeps the
  # injection surface zero even if the regex is ever loosened.
  WS_ID_FOR_PY="$WS_ID" "$VENV_PY" -c '
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
# 7. exec the Semantica explorer. Manager passes the port as $1.
#    The venv's python has the wheel installed (Stage 4) so
#    `python -m semantica.explorer` resolves from the venv
#    site-packages, not from PYTHONPATH or a cwd cd.
# ------------------------------------------------------------------
exec "$VENV_PY" -m semantica.explorer \
  --graph "$GRAPH_PATH" \
  --port "$1" \
  --host 127.0.0.1 \
  --no-browser
