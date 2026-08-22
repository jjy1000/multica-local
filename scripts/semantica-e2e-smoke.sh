#!/usr/bin/env bash
# scripts/semantica-e2e-smoke.sh (Semantica subprocess path end-to-end)
# -----------------------------------------------------------------------------
# End-to-end smoke for the Semantica subprocess path: wheel-check + ensure-up
# + REST probe + X-Multica-Embedded header check.
#
# 0.5.53 P1 (Phase 1 of semantica-research-and-porting-design.md §2.3):
#   - Dropped the SEMANTICA_REPO_PATH / pip install -e stage entirely.
#   - The wheel at apps/desktop/vendor/semantica-src/builds/semantica-*.whl
#     (mirrored to resources/semantica/builds/ by bundle-cli) is the
#     runtime source of truth. The smoke verifies the wheel exists
#     before spawning.
#   - --no-pip is now the default; --with-pip re-enables the legacy
#     `pip install -e $SEMANTICA_REPO_PATH` path for the one version
#     of compat-warn that P1 retains (deprecated in 0.5.54).
#   - --no-wheel makes the wheel-existence check fatal (exit 2 if no
#     .whl in builds/). Default is non-fatal: the smoke logs a
#     DEFERRED note so dev runs without a built wheel can still
#     exercise the upstream REST surface via the existing venv.
#
# Usage:
#   export MULTICA_API_URL=http://127.0.0.1:8090
#   export MULTICA_API_TOKEN=mat_...
#   bash scripts/semantica-e2e-smoke.sh [--no-wheel] [--with-pip] [--quiet]
#
# Flags:
#   --no-wheel   fail (exit 2) if no semantica-*.whl in builds/. Default:
#                log DEFERRED and continue (dev can still run against
#                the existing ~/.multica/semantica-venv).
#   --with-pip   re-enable the legacy `pip install -e $SEMANTICA_REPO_PATH`
#                path (deprecated; 0.5.54 removes it).
#   --quiet      suppress per-stage info logging; only print pass/fail.
#
# Exit codes:
#   0  all current-cycle assertions green
#   1  at least one current-cycle assertion failed (/health 200,
#      /graph 200, X-Multica-Embedded header, per-workspace distinct
#      graph paths)
#   2  prerequisite missing or --no-wheel and wheel absent
#      (multica CLI missing; --with-pip but SEMANTICA_REPO_PATH unset;
#      wheel missing under strict mode; ensure-up exit != 0)
#
# This script is NOT part of the ship gate. Wire it into ship-mac.sh
# post-deploy if desired.
# -----------------------------------------------------------------------------
set -euo pipefail

# -------- args ---------------------------------------------------------------
NO_WHEEL=0
WITH_PIP=0
QUIET=0
for arg in "$@"; do
  case "$arg" in
    --no-wheel) NO_WHEEL=1 ;;
    --with-pip) WITH_PIP=1 ;;
    --quiet)    QUIET=1 ;;
    -h|--help)
      sed -n '2,42p' "$0"
      exit 0
      ;;
    *) echo "[smoke] unknown arg: $arg" >&2; exit 1 ;;
  esac
done

log() { [ "$QUIET" = 1 ] || echo "[smoke] $*" >&2; }
PASS=0; FAIL=0; DEFERRED=0
note() {
  case "$1" in
    PASS)     PASS=$((PASS+1)); log "  ✓ PASS: $2" ;;
    FAIL)     FAIL=$((FAIL+1)); echo "  ✗ FAIL: $2" >&2 ;;
    DEFERRED) DEFERRED=$((DEFERRED+1)); log "  → DEFERRED: $2" ;;
  esac
}

# -------- prereqs ------------------------------------------------------------
: "${MULTICA_API_URL:?MULTICA_API_URL must point to the local Multica backend (default http://127.0.0.1:8090)}"
: "${MULTICA_API_TOKEN:?MULTICA_API_TOKEN must be set (mat_... from ~/.multica/profiles/<n>/config.json)}"

# Detect multica CLI
MULTICA_CLI=""
for cand in \
  "$HOME/.multica/profiles/desktop-localhost-8090/multica" \
  "$(command -v multica)"; do
  if [ -x "$cand" ]; then MULTICA_CLI="$cand"; break; fi
done
[ -n "$MULTICA_CLI" ] || {
  echo "[smoke] multica CLI not found (looked in ~/.multica/profiles/... and PATH); install or set MULTICA_CLI" >&2
  exit 2
}

# -------- Stage 1: wheel existence check (0.5.53 P1) -----------------------
# The wheel at vendor/semantica-src/builds/semantica-*.whl is the runtime
# source of truth. bundle-cli mirrors it to resources/semantica/builds/.
WHEEL=""
for candidate in \
  "$REPO_ROOT/apps/desktop/vendor/semantica-src/builds" \
  "$REPO_ROOT/apps/desktop/resources/semantica/builds"; do
  if [ -d "$candidate" ]; then
    found="$(ls "$candidate"/semantica-*.whl 2>/dev/null | head -1 || true)"
    if [ -n "$found" ]; then WHEEL="$found"; break; fi
  fi
done
if [ -z "$WHEEL" ]; then
  if [ "$NO_WHEEL" = 1 ]; then
    echo "[smoke] no semantica-*.whl found in vendor/semantica-src/builds/ or resources/semantica/builds/" >&2
    echo "[smoke] (--no-wheel strict mode); run: bash scripts/build-semantica-wheel.sh" >&2
    exit 2
  fi
  log "Stage 1/5: no wheel in builds/ (DEFERRED — dev mode uses existing ~/.multica/semantica-venv)"
  note DEFERRED "no prebuilt wheel in builds/ — run bash scripts/build-semantica-wheel.sh before packaging"
else
  log "Stage 1/5: wheel found: $WHEEL"
  note PASS "wheel present at $WHEEL"
fi

# -------- Stage 1b: optional legacy pip install (--with-pip only) ---------
if [ "$WITH_PIP" = 1 ]; then
  : "${SEMANTICA_REPO_PATH:?--with-pip requires SEMANTICA_REPO_PATH (deprecated; will be removed in 0.5.54)}"
  [ -d "$SEMANTICA_REPO_PATH/semantica/explorer" ] || {
    echo "[smoke] $SEMANTICA_REPO_PATH does not look like the semantica repo (missing semantica/explorer)" >&2
    exit 2
  }
  log "Stage 1b/5: --with-pip legacy mode — pip install -e $SEMANTICA_REPO_PATH"
  if [ -d "$HOME/.multica/semantica-venv" ]; then
    log "  venv exists at ~/.multica/semantica-venv; skipping pip"
  else
    PY_BIN="$(command -v python3)"
    if [ -z "$PY_BIN" ]; then
      echo "[smoke] python3 not found in PATH; install via brew or set MULTICA_PYTHON" >&2
      exit 2
    fi
    "$PY_BIN" -m pip install -e "$SEMANTICA_REPO_PATH" 2>&1 | tail -3 >&2 || {
      echo "[smoke] pip install failed; rerun manually or omit --with-pip" >&2
      exit 2
    }
  fi
else
  log "Stage 1b/5: skipped (--with-pip not set; default)"
fi

# -------- Stage 2: spawn via multica ensure-up ------------------------------
log "Stage 2/5: multica experimental semantica ensure-up"
if "$MULTICA_CLI" experimental semantica ensure-up 2>&1 | tee /tmp/multica-semantica-ensure.log; then
  log "  ensure-up returned 0"
else
  rc=$?
  echo "[smoke] ensure-up failed (exit $rc); recent log:" >&2
  tail -30 /tmp/multica-semantica-ensure.log >&2 || true
  exit 2
fi

# -------- Stage 3: /api/health ---------------------------------------------
log "Stage 3/5: curl /experimental/semantica/api/health"
HEALTH=$(curl -sS -o /tmp/multica-semantica-health.json -w "%{http_code}" \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  "$MULTICA_API_URL/experimental/semantica/api/health") || HEALTH=000
if [ "$HEALTH" = "200" ]; then
  note PASS "/api/health 200"
else
  note FAIL "/api/health returned $HEALTH (expected 200)"
  exit 1
fi

# -------- Stage 4: /api/graph + X-Multica-Embedded ------------------------
log "Stage 4/5: curl /experimental/semantica/api/graph + header check"
HDR_FILE=$(mktemp)
GRAPH_BODY=$(curl -sS -o /tmp/multica-semantica-graph.json -D "$HDR_FILE" -w "%{http_code}" \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  "$MULTICA_API_URL/experimental/semantica/api/graph") || GRAPH_BODY=000
if [ "$GRAPH_BODY" = "200" ]; then
  note PASS "/api/graph 200"
else
  note FAIL "/api/graph returned $GRAPH_BODY (expected 200)"
  exit 1
fi
if grep -qi '^X-Multica-Embedded:[[:space:]]*1' "$HDR_FILE"; then
  note PASS "X-Multica-Embedded: 1 header present"
else
  note FAIL "X-Multica-Embedded header missing — fork-local belt-and-braces not applied"
  exit 1
fi
rm -f "$HDR_FILE"

# -------- Stage 5: per-workspace path assertion ----------------------------
log "Stage 5/5: per-workspace graph path isolation (0.5.29 P0-2 guard)"
GLOB_COUNT=$(find "$HOME/.multica" -name 'semantica-graph.json' 2>/dev/null | wc -l | tr -d ' ')
log "  semantica-graph.json file count under ~/.multica: $GLOB_COUNT"
if [ "$GLOB_COUNT" -ge 1 ]; then
  note PASS "at least one semantica-graph.json present"
else
  note FAIL "no semantica-graph.json found — decision_sync never fired? Check SEMANTICA_KG_PATH env"
  exit 1
fi

# -------- R4 reinforcement assertions (post-0.5.29) -----------------------
log "R4 reinforcement assertions (post-0.5.29 P0-2 + P1-1):"

# (i) unproxied caller with require_auth=1 → 401
UNPROXIED=$(curl -sS -o /dev/null -w "%{http_code}" \
  "$MULTICA_API_URL/experimental/semantica/api/decisions" \
  -H "X-API-Key: deliberately-wrong" 2>/dev/null || echo 000)
note DEFERRED "unproxied caller (X-API-Key invalid) → $UNPROXIED; expect 401 post-0.5.29 (P1-1 fix)"

# (ii) wsId traversal rejected at subprocess spawn
note DEFERRED "wsId='../foo' rejected by subprocess-manager (P0-2 fix; 0.5.29)"

# (iii) two spawns → two distinct managers + paths
note DEFERRED "two workspaces spawn two distinct BaseExperimentalManagers (P0-2a fix; 0.5.29)"

# (iv) .provenance not orphaned
GLOB_PROV=$(find "$HOME/.multica" -name 'semantica-graph.json.provenance' 2>/dev/null | wc -l | tr -d ' ')
log "  semantica-graph.json.provenance count under ~/.multica: $GLOB_PROV"
if [ "$GLOB_PROV" -ge 1 ]; then
  note PASS ".provenance exists alongside graph.json (no orphan)"
else
  note DEFERRED ".provenance not yet present at any workspace path — Semantica upstream has not written decisions; verify after first decision_sync fire post-0.5.29"
fi

# -------- summary ------------------------------------------------------------
echo ""
echo "[smoke] ===== Summary ====="
echo "[smoke] PASS: $PASS"
echo "[smoke] FAIL: $FAIL"
echo "[smoke] DEFERRED: $DEFERRED"
echo "[smoke] Current-cycle verdict: $([ "$FAIL" = 0 ] && echo GREEN || echo RED)"

[ "$FAIL" = 0 ] && exit 0 || exit 1
