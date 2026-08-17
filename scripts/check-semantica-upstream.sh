#!/usr/bin/env bash
# scripts/check-semantica-upstream.sh (Semantica upstream-watch delta diff)
# -----------------------------------------------------------------------------
# Mechanical diff between the upstream `semantica-agi/semantica` CHANGELOG and
# the fork-local endpoint inventory in api-source-map.md. Synthesizer Round 7
# plan item C — companion to .omc/semantica-upstream-watch.md.
#
# Workflow (monthly cadence):
#   1. Fetch upstream CHANGELOG.md via curl.
#   2. Extract /api/* endpoint literals (grep -E).
#   3. Extract fork-local endpoint inventory from api-source-map.md.
#   4. Print NEW ENDPOINTS (upstream-only) + REMOVED (fork-only).
#   5. Exit 0 on no-diff or successful "nothing new" report; exit 1 on network
#      failure; exit 2 on missing prerequisites.
#
# Constraints (per Synthesizer spec):
#   - No jq, no Python. curl + grep + awk + sort + comm only.
#   - bash -n clean, shellcheck-clean.
#   - Re-runnable: idempotent, no side-effects beyond a tmp file + stdout.
#   - Network failure must NOT silently pass — exit 1.
#
# Usage:
#   bash scripts/check-semantica-upstream.sh
#   bash scripts/check-semantica-upstream.sh --quiet     # suppress headers
#   bash scripts/check-semantica-upstream.sh --notify    # macOS notification on diff
#
# Exit codes:
#   0  no diff detected (or successful silent run)
#   1  network failure (curl non-zero, non-404 — e.g. DNS, TLS, 5xx)
#   2  prerequisite missing (curl/grep/awk absent; api-source-map.md not found)
# -----------------------------------------------------------------------------
set -euo pipefail

# -------- args ---------------------------------------------------------------
QUIET=0
NOTIFY=0
for arg in "$@"; do
  case "$arg" in
    --quiet)  QUIET=1 ;;
    --notify) NOTIFY=1 ;;
    -h|--help)
      sed -n '2,30p' "$0"
      exit 0
      ;;
    *) echo "[upstream-watch] unknown arg: $arg" >&2; exit 2 ;;
  esac
done

log() { [ "$QUIET" = 1 ] || echo "[upstream-watch] $*" >&2; }

# -------- prereqs ------------------------------------------------------------
for bin in curl grep awk sort comm mktemp; do
  command -v "$bin" >/dev/null 2>&1 || {
    echo "[upstream-watch] required binary not found: $bin" >&2
    exit 2
  }
done

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || true)"
if [ -z "$REPO_ROOT" ]; then
  echo "[upstream-watch] not inside a git repository; cannot locate api-source-map.md" >&2
  exit 2
fi

API_SOURCE_MAP="$REPO_ROOT/server/internal/service/builtin_skills/multica-semantica-decision-advisor/references/api-source-map.md"
[ -f "$API_SOURCE_MAP" ] || {
  echo "[upstream-watch] api-source-map.md not found at $API_SOURCE_MAP" >&2
  exit 2
}

# Upstream source. Override via env for forks/mirrors.
UPSTREAM_URL="${SEMANTICA_CHANGELOG_URL:-https://raw.githubusercontent.com/semantica-agi/semantica/main/CHANGELOG.md}"

# -------- Stage 1: fetch upstream CHANGELOG ----------------------------------
TMP_CHANGELOG="$(mktemp -t semantica-changelog.XXXXXX)"
trap 'rm -f "$TMP_CHANGELOG" "$TMP_UPSTREAM_ENDPOINTS" "$TMP_FORK_ENDPOINTS"' EXIT

log "fetching $UPSTREAM_URL"
HTTP_CODE="$(curl -sS -L -o "$TMP_CHANGELOG" -w '%{http_code}' \
  --max-time 30 \
  "$UPSTREAM_URL" 2>/dev/null || echo "000")"

# 200 = OK; 404 = upstream not yet published (treat as no-diff, not network err);
# anything else (000, 5xx, TLS) = network failure.
case "$HTTP_CODE" in
  200) log "  fetched ($(wc -c <"$TMP_CHANGELOG" | tr -d ' ') bytes)" ;;
  404) log "  upstream CHANGELOG not found (404); assuming no diff this cycle"
       exit 0 ;;
  *)   echo "[upstream-watch] network failure: HTTP $HTTP_CODE from $UPSTREAM_URL" >&2
       exit 1 ;;
esac

# -------- Stage 2: extract /api/* endpoints ----------------------------------
# Match `/api/<segment>(/<segment>)?` — covers /api/decisions, /api/decisions/{id},
# /api/ontology/shacl/shapes, etc. Path params in {} are kept verbatim.
TMP_UPSTREAM_ENDPOINTS="$(mktemp -t semantica-up.XXXXXX)"
grep -oE '/api/[a-zA-Z][a-zA-Z0-9_-]*(/[a-zA-Z{][a-zA-Z0-9{}_-]*)*' \
  "$TMP_CHANGELOG" \
  | awk '{print tolower($0)}' \
  | sort -u > "$TMP_UPSTREAM_ENDPOINTS" || true

log "upstream endpoints in CHANGELOG: $(wc -l <"$TMP_UPSTREAM_ENDPOINTS" | tr -d ' ')"

# -------- Stage 3: extract fork-local inventory ------------------------------
# api-source-map.md tables are `| Verb | Method | Path | Required fields |`.
# Grep the path column (4th pipe-delimited field), strip backticks + query
# strings, normalize to a bare path.
TMP_FORK_ENDPOINTS="$(mktemp -t semantica-fork.XXXXXX)"
awk -F'|' '
  /^\|.*\|.*\| `/ {
    path = $4
    gsub(/^ +| +$/, "", path)
    gsub(/`/, "", path)
    # drop query string
    sub(/\?.*/, "", path)
    # strip path params so /decisions/{id} matches /decisions (we only care
    # about endpoint surfaces, not IDs)
    gsub(/\/{[a-zA-Z_]+}/, "", path)
    if (path ~ /^\/[a-zA-Z]/) print tolower(path)
  }
' "$API_SOURCE_MAP" \
  | sort -u > "$TMP_FORK_ENDPOINTS" || true

log "fork-local endpoints in api-source-map.md: $(wc -l <"$TMP_FORK_ENDPOINTS" | tr -d ' ')"

# Normalize upstream paths the same way so the comm comparison is shape-faithful
TMP_UPSTREAM_NORM="$(mktemp -t semantica-up-norm.XXXXXX)"
sed -e 's/{[a-zA-Z_]*}//g' "$TMP_UPSTREAM_ENDPOINTS" | sort -u > "$TMP_UPSTREAM_NORM"
trap 'rm -f "$TMP_CHANGELOG" "$TMP_UPSTREAM_ENDPOINTS" "$TMP_UPSTREAM_NORM" "$TMP_FORK_ENDPOINTS"' EXIT

NEW_ENDPOINTS="$(comm -23 "$TMP_UPSTREAM_NORM" "$TMP_FORK_ENDPOINTS" || true)"
REMOVED_ENDPOINTS="$(comm -13 "$TMP_UPSTREAM_ENDPOINTS" "$TMP_FORK_ENDPOINTS" || true)"

NEW_COUNT="$(printf '%s\n' "$NEW_ENDPOINTS" | grep -c . || true)"
REMOVED_COUNT="$(printf '%s\n' "$REMOVED_ENDPOINTS" | grep -c . || true)"

echo ""
echo "[upstream-watch] ===== Summary ====="
echo "[upstream-watch] NEW (upstream-only): $NEW_COUNT"
echo "[upstream-watch] REMOVED (fork-only): $REMOVED_COUNT"

if [ "$NEW_COUNT" -gt 0 ]; then
  echo ""
  echo "[upstream-watch] NEW ENDPOINTS:"
  printf '  %s\n' $NEW_ENDPOINTS
fi

if [ "$REMOVED_COUNT" -gt 0 ]; then
  echo ""
  echo "[upstream-watch] REMOVED ENDPOINTS (upstream deprecated):"
  printf '  %s\n' $REMOVED_ENDPOINTS
fi

# -------- Stage 5: notify on diff -------------------------------------------
if [ "$NOTIFY" = 1 ] && [ "$NEW_COUNT" -gt 0 ] && command -v osascript >/dev/null 2>&1; then
  osascript -e "display notification \"$NEW_COUNT new Semantica endpoint(s) detected\" with title \"Multica upstream watch\"" \
    >/dev/null 2>&1 || true
fi

[ "$NEW_COUNT" -eq 0 ] && [ "$REMOVED_COUNT" -eq 0 ] && log "no diff detected" && exit 0
exit 0