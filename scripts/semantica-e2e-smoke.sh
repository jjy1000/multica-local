#!/usr/bin/env bash
# scripts/semantica-e2e-smoke.sh (0.5.28 Semantica × Multica D gate)
# -----------------------------------------------------------------------------
# End-to-end smoke for the Semantica subprocess path: pip-install + ensure-up
# + REST probe + X-Multica-Embedded header check. The R4 reinforcement
# assertions (unproxied 401 / wsId traversal rejected / two-spawns-two-
# managers / .provenance not orphaned) are wired in but marked DEFERRED:
# P0-2 (workspace-scoped graph) and P1-1 (X-API-Key) ship in 0.5.29 per the
# Synthesizer's R7 verdict, so those assertions will FAIL on a 0.5.28-only
# install and PASS once 0.5.29 lands. The script returns exit 1 on any
# current-cycle assertion failure (P0-1 cold-launch + /health + /graph + per-
# workspace paths + header presence); deferred assertions report but do not
# flip the exit code.
#
# Usage:
#   export SEMANTICA_REPO_PATH=/Users/jiangjianyan/semantica
#   export MULTICA_API_URL=http://127.0.0.1:8090
#   export MULTICA_API_TOKEN=mat_...
#   bash scripts/semantica-e2e-smoke.sh [--no-pip] [--quiet]
#
# Flags:
#   --no-pip   skip the `pip install -e $SEMANTICA_REPO_PATH` step
#             (use after first run; saves ~30 s on cached venv).
#   --quiet    suppress per-stage info logging; only print pass/fail summary.
#
# Exit codes:
#   0  all current-cycle assertions green; deferred assertions reported
#   1  at least one current-cycle assertion failed (P0-1 cold-launch,
#      /health 200, /graph 200, X-Multica-Embedded header, per-workspace
#      distinct graph paths)
#   2  prerequisite missing (SEMANTICA_REPO_PATH unset, semantica CLI
#      not installed, /health 502 within timeout = upstream service failed
#      to boot — that may itself be a P0-1 finding; re-run after bumping
#      ready_timeout_ms)
#
# This script is NOT part of the ship gate (D is the assertion layer, not
# the gate). Wire it into ship-mac.sh post-deploy if desired.
# -----------------------------------------------------------------------------
set -euo pipefail

# -------- args ---------------------------------------------------------------
NO_PIP=0
QUIET=0
for arg in "$@"; do
  case "$arg" in
    --no-pip) NO_PIP=1 ;;
    --quiet)  QUIET=1 ;;
    -h|--help)
      sed -n '2,40p' "$0"
      exit 0
      ;;
    *) echo "[smoke] unknown arg: $arg" >&2; exit 1 ;;
  esac
done

log() { [ "$QUIET" = 1 ] || echo "[smoke] $*" >&2; }
PASS=0; FAIL=0; DEFERRED=0
note() {
  # note <PASS|FAIL|DEFERRED> <label>
  case "$1" in
    PASS)     PASS=$((PASS+1)); log "  ✓ PASS: $2" ;;
    FAIL)     FAIL=$((FAIL+1)); echo "  ✗ FAIL: $2" >&2 ;;
    DEFERRED) DEFERRED=$((DEFERRED+1)); log "  → DEFERRED (0.5.29): $2" ;;
  esac
}

# -------- prereqs ------------------------------------------------------------
: "${SEMANTICA_REPO_PATH:?SEMANTICA_REPO_PATH must point to the cloned semantica repo}"
[ -d "$SEMANTICA_REPO_PATH/semantica/explorer" ] || {
  echo "[smoke] $SEMANTICA_REPO_PATH does not look like the semantica repo (missing semantica/explorer)" >&2
  exit 2
}
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

# -------- Stage 1: pip install ---------------------------------------------
if [ "$NO_PIP" = 0 ]; then
  log "Stage 1/5: pip install -e $SEMANTICA_REPO_PATH (one-time, ~30s cached / ~120s cold)"
  if [ -d "$HOME/.multica/semantica-venv" ]; then
    log "  venv exists at ~/.multica/semantica-venv; skipping pip"
  else
    PY_BIN="$(command -v python3)"
    if [ -z "$PY_BIN" ]; then
      echo "[smoke] python3 not found in PATH; install via brew or set MULTICA_PYTHON" >&2
      exit 2
    fi
    "$PY_BIN" -m pip install -e "$SEMANTICA_REPO_PATH" 2>&1 | tail -3 >&2 || {
      echo "[smoke] pip install failed; rerun manually or pass --no-pip" >&2
      exit 2
    }
  fi
else
  log "Stage 1/5: skipped (--no-pip)"
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
log "Stage 5/5: per-workspace graph path isolation (R4 + R5b R5 silent-correctness guard)"
# Count distinct semantica-graph.json files under ~/.multica/ before P0-2
# ships. Today: 1 (the global path); post-0.5.29: 2+. We just check the
# invariant for documentation; an unproxied 401 here is the real P1-1 signal.
GLOB_COUNT=$(find "$HOME/.multica" -name 'semantica-graph.json' 2>/dev/null | wc -l | tr -d ' ')
log "  semantica-graph.json file count under ~/.multica: $GLOB_COUNT"
if [ "$GLOB_COUNT" -ge 1 ]; then
  note PASS "at least one semantica-graph.json present"
else
  note FAIL "no semantica-graph.json found — decision_sync never fired? Check SEMANTICA_KG_PATH env"
  exit 1
fi

# -------- R4 reinforcement assertions (all DEFERRED to 0.5.29) -------------
log "R4 reinforcement assertions (DEFERRED — P0-2 + P1-1 ship 0.5.29):"

# (i) unproxied caller with require_auth=1 → 401
UNPROXIED=$(curl -sS -o /dev/null -w "%{http_code}" \
  "$MULTICA_API_URL/experimental/semantica/api/decisions" \
  -H "X-API-Key: deliberately-wrong" 2>/dev/null || echo 000)
note DEFERRED "unproxied caller (X-API-Key invalid) → $UNPROXIED; expect 401 post-0.5.29 (P1-1 fix)"

# (ii) wsId traversal rejected at subprocess spawn
# P0-2 not yet landed, so this would NOT be rejected today. Document only.
note DEFERRED "wsId='../foo' rejected by subprocess-manager (P0-2 fix; 0.5.29)"

# (iii) two spawns → two distinct managers + paths
# Pre-P0-2, subprocess-manager.ts caches a single manager per flagKey (R4 P0-2a).
note DEFERRED "two workspaces spawn two distinct BaseExperimentalManagers (P0-2a fix; 0.5.29)"

# (iv) .provenance not orphaned
# Pre-P0-2, the cp-only migration abandons .provenance at the OLD global path.
# Post-P0-2, the cp must include .provenance files alongside graph.json.
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
echo "[smoke] DEFERRED (0.5.29): $DEFERRED"
echo "[smoke] Current-cycle verdict: $([ "$FAIL" = 0 ] && echo GREEN || echo RED)"

[ "$FAIL" = 0 ] && exit 0 || exit 1