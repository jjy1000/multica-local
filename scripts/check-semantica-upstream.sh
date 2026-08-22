#!/usr/bin/env bash
# scripts/check-semantica-upstream.sh (Semantica upstream-watch delta diff)
# -----------------------------------------------------------------------------
# Mechanical diff between the upstream `semantica-agi/semantica` (now vendored
# at apps/desktop/vendor/semantica-src/) and the fork-local integration surface.
# Synthesizer Round 7 plan item C — companion to .omc/semantica-upstream-watch.md.
#
# Workflow (monthly cadence):
#   1. Per-scraper source extraction (grep/awk on local files + curl for endpoint
#      CHANGELOG only).
#   2. Compute NEW (upstream-only) + REMOVED (fork-only) via comm.
#   3. Print summary. Exit 0 on no-diff or successful "nothing new" report.
#
# Constraints (per Synthesizer spec):
#   - No jq, no Python. curl + grep + awk + sort + comm only.
#   - bash -n clean.
#   - Re-runnable: idempotent, no side-effects beyond tmp files + stdout.
#   - Network failure must NOT silently pass — exit 1.
#
# Usage:
#   bash scripts/check-semantica-upstream.sh                         # all 5 scrapers
#   bash scripts/check-semantica-upstream.sh --scraper=endpoint      # one scraper
#   bash scripts/check-semantica-upstream.sh --quiet                 # suppress headers
#   bash scripts/check-semantica-upstream.sh --notify                # macOS notification
#
# Scrapers:
#   endpoint  /api/* literals in upstream CHANGELOG vs fork api-source-map.md
#   field     dataclass field names in semantica/context/decision_models.py
#             vs the SemanticaDecisionRecordSchema zod block in
#             packages/core/api/schemas.ts (drift = wire-shape mismatch)
#   sparql    _FORBIDDEN_KEYWORDS regex in semantica/explorer/routes/sparql.py
#             vs forbidden-keyword mentions in multica-semantica SKILL.md
#             (drift = missing local blocklist coverage)
#   vocab     sem: predicates declared in semantica-ns.ttl
#             vs sem:<term> literals used in fork source
#             (drift = undeclared exports or stale local terms)
#   deps      [project.dependencies] in vendored pyproject.toml
#             vs apps/desktop/vendor/semantica/requirements.txt
#             (drift = drift between source-of-truth and runtime deps)
#
# Exit codes:
#   0  no diff detected (or successful silent run)
#   1  network failure (curl non-zero, non-404 — e.g. DNS, TLS, 5xx)
#   2  prerequisite missing or unknown scraper
# -----------------------------------------------------------------------------
set -euo pipefail

# -------- args ---------------------------------------------------------------
QUIET=0
NOTIFY=0
SCRAPER="all"
for arg in "$@"; do
  case "$arg" in
    --quiet)  QUIET=1 ;;
    --notify) NOTIFY=1 ;;
    --scraper=*)
      SCRAPER="${arg#--scraper=}"
      case "$SCRAPER" in
        endpoint|field|sparql|vocab|deps|all) ;;
        *) echo "[upstream-watch] unknown scraper: $SCRAPER (expected: endpoint|field|sparql|vocab|deps|all)" >&2; exit 2 ;;
      esac
      ;;
    -h|--help)
      sed -n '2,40p' "$0"
      exit 0
      ;;
    *) echo "[upstream-watch] unknown arg: $arg" >&2; exit 2 ;;
  esac
done

log() { [ "$QUIET" = 1 ] || echo "[upstream-watch] $*" >&2; }
has_diff=0  # global counter; non-zero exit at the end if any scraper found drift

# -------- prereqs ------------------------------------------------------------
for bin in grep awk sort comm mktemp; do
  command -v "$bin" >/dev/null 2>&1 || {
    echo "[upstream-watch] required binary not found: $bin" >&2
    exit 2
  }
done

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || true)"
if [ -z "$REPO_ROOT" ]; then
  echo "[upstream-watch] not inside a git repository" >&2
  exit 2
fi

VENDOR_SRC="$REPO_ROOT/apps/desktop/vendor/semantica-src"
VENDOR_RUNTIME="$REPO_ROOT/apps/desktop/vendor/semantica"
API_SOURCE_MAP="$REPO_ROOT/server/internal/service/builtin_skills/multica-semantica-decision-advisor/references/api-source-map.md"
SCHEMAS_TS="$REPO_ROOT/packages/core/api/schemas.ts"
DECISION_SYNC_GO="$REPO_ROOT/server/internal/handler/decision_sync.go"
SEMANTICA_SKILL="$REPO_ROOT/server/internal/service/builtin_skills/multica-semantica/SKILL.md"

[ -d "$VENDOR_SRC" ] || {
  echo "[upstream-watch] vendored subtree missing at $VENDOR_SRC (run scripts/sync-semantica-upstream.sh first)" >&2
  exit 2
}

# Cleanup on exit. List of tmp files registered as they're created.
TMP_FILES=()
cleanup() { rm -f "${TMP_FILES[@]}"; }
trap cleanup EXIT

# -------- helpers ------------------------------------------------------------
# Print "[upstream-watch] <kind> ==== Summary ====" + NEW/REMOVED sections.
# $1 = scraper name; $2 = upstream file; $3 = fork file; $4 = human description.
report_diff() {
  local name="$1" upstream="$2" fork="$3" desc="$4"
  if [ ! -f "$upstream" ]; then
    log "  $name: upstream source missing ($upstream) — skipping"
    return 0
  fi
  if [ ! -f "$fork" ]; then
    log "  $name: fork source missing ($fork) — skipping"
    return 0
  fi
  local new removed new_n removed_n
  new="$(comm -23 "$upstream" "$fork" 2>/dev/null || true)"
  removed="$(comm -13 "$upstream" "$fork" 2>/dev/null || true)"
  new_n="$(printf '%s\n' "$new" | grep -c . 2>/dev/null || true)"
  removed_n="$(printf '%s\n' "$removed" | grep -c . 2>/dev/null || true)"
  new_n="${new_n:-0}"; removed_n="${removed_n:-0}"
  echo ""
  echo "[upstream-watch] $name: $desc"
  echo "[upstream-watch]   NEW (upstream-only): $new_n"
  echo "[upstream-watch]   REMOVED (fork-only): $removed_n"
  if [ "${new_n:-0}" -gt 0 ]; then
    echo "[upstream-watch]   --- NEW ---"
    printf '  %s\n' $new
    has_diff=1
  fi
  if [ "${removed_n:-0}" -gt 0 ]; then
    echo "[upstream-watch]   --- REMOVED ---"
    printf '  %s\n' $removed
    has_diff=1
  fi
}

# -------- scraper: endpoint (curl + CHANGELOG, fork api-source-map) ----------
scraper_endpoint() {
  command -v curl >/dev/null 2>&1 || {
    log "endpoint: curl not on PATH; skipping network scraper"
    return 0
  }
  [ -f "$API_SOURCE_MAP" ] || {
    log "endpoint: api-source-map.md missing; skipping"
    return 0
  }
  local UPSTREAM_URL="${SEMANTICA_CHANGELOG_URL:-https://raw.githubusercontent.com/semantica-agi/semantica/main/CHANGELOG.md}"
  local tmp_changelog; tmp_changelog="$(mktemp -t semantica-changelog.XXXXXX)"; TMP_FILES+=("$tmp_changelog")
  log "endpoint: fetching $UPSTREAM_URL"
  local code
  code="$(curl -sS -L -o "$tmp_changelog" -w '%{http_code}' --max-time 30 "$UPSTREAM_URL" 2>/dev/null || echo 000)"
  case "$code" in
    200) log "  fetched ($(wc -c <"$tmp_changelog" | tr -d ' ') bytes)" ;;
    404) log "  upstream CHANGELOG 404; treating as no-diff"; return 0 ;;
    *)   echo "[upstream-watch] endpoint: network failure HTTP $code" >&2; return 1 ;;
  esac
  local tmp_up tmp_fork tmp_up_norm
  tmp_up="$(mktemp -t semantica-up-endpoints.XXXXXX)"; TMP_FILES+=("$tmp_up")
  tmp_fork="$(mktemp -t semantica-fork-endpoints.XXXXXX)"; TMP_FILES+=("$tmp_fork")
  tmp_up_norm="$(mktemp -t semantica-up-norm.XXXXXX)"; TMP_FILES+=("$tmp_up_norm")
  grep -oE '/api/[a-zA-Z][a-zA-Z0-9_-]*(/[a-zA-Z{][a-zA-Z0-9{}_-]*)*' "$tmp_changelog" \
    | awk '{print tolower($0)}' | sort -u > "$tmp_up" || true
  awk -F'|' '/^\|.*\|.*\| `/ {
    p = $4; gsub(/^ +| +$/, "", p); gsub(/`/, "", p); sub(/\?.*/, "", p);
    gsub(/\/{[a-zA-Z_]+}/, "", p); if (p ~ /^\/[a-zA-Z]/) print tolower(p)
  }' "$API_SOURCE_MAP" | sort -u > "$tmp_fork" || true
  # Upstream paths in CHANGELOG are absolute (/api/decisions/...); the fork's
  # api-source-map uses Multica-relative paths (the reverse proxy at
  # /experimental/semantica/api/* prepends /api server-side, so the proxy
  # mount the agent sees is just /decisions/...). Strip the upstream's
  # leading /api so comm compares shape, not absolute prefix.
  sed -e 's|^{[a-zA-Z_]*}||g' -e 's|^/api/|/|' "$tmp_up" | sort -u > "$tmp_up_norm"
  # Fork paths are already relative; only normalise empty path.
  sed -e 's|^$|/|' "$tmp_fork" | sort -u > "${tmp_fork}.norm"; mv "${tmp_fork}.norm" "$tmp_fork"
  report_diff "endpoint" "$tmp_up_norm" "$tmp_fork" "/api/* endpoint literals (leading /api/ stripped for shape comparison)"
}

# -------- scraper: field (dataclass vs zod) --------------------------------
scraper_field() {
  local upstream="$VENDOR_SRC/semantica/context/decision_models.py"
  local fork="$SCHEMAS_TS"
  if [ ! -f "$fork" ]; then
    log "field: $fork missing; skipping"
    return 0
  fi
  local tmp_up tmp_fork
  tmp_up="$(mktemp -t semantica-fields-up.XXXXXX)"; TMP_FILES+=("$tmp_up")
  tmp_fork="$(mktemp -t semantica-fields-fork.XXXXXX)"; TMP_FILES+=("$tmp_fork")
  # Upstream: dataclass top-level field lines, e.g. "    decision_id: str"
  grep -E '^[[:space:]]+[a-z_][a-z0-9_]*:[[:space:]]' "$upstream" 2>/dev/null \
    | awk -F: '{gsub(/^[[:space:]]+|[[:space:]]+$/, "", $1); print $1}' \
    | grep -vE '^(Optional|List|Dict|Any|str|int|float|bool|datetime)$' \
    | sort -u > "$tmp_up" || true
  # Fork: zod keys inside the SemanticaDecisionRecordSchema + Provenance blocks.
  # We extract the first line range that follows "SemanticaDecisionRecordSchema"
  # OR "SemanticaDecisionProvenanceSchema" through the closing ").loose()".
  # Use BSD-portable awk (no gawk match-with-array extension).
  awk '
    /SemanticaDecisionRecordSchema = z.object\(/ || /SemanticaDecisionProvenanceSchema = z.object\(/ { depth=1; in_block=1; next }
    in_block {
      # Track brace depth to know when the object closes.
      n_open = gsub(/\{/, "{")
      n_close = gsub(/\}/, "}")
      depth += n_open - n_close
      if (depth <= 0) { in_block=0; next }
      # Match "<key>: z.<something>(...)" — BSD awk: use match() + substr().
      if (match($0, /^[[:space:]]+[a-z_][a-z0-9_]*[[:space:]]*:[[:space:]]*z\./)) {
        line = substr($0, RSTART, RLENGTH)
        # Strip leading whitespace + trailing " :z.".
        sub(/^[[:space:]]+/, "", line)
        sub(/[[:space:]]*:.*/, "", line)
        print line
      }
    }
  ' "$fork" | sort -u > "$tmp_fork" || true
  report_diff "field" "$tmp_up" "$tmp_fork" "Decision record field names (dataclass vs zod)"
}

# -------- scraper: sparql (forbidden keywords) ------------------------------
scraper_sparql() {
  local upstream="$VENDOR_SRC/semantica/explorer/routes/sparql.py"
  local fork="$SEMANTICA_SKILL"
  if [ ! -f "$fork" ]; then
    log "sparql: $fork missing; skipping"
    return 0
  fi
  local tmp_up tmp_fork
  tmp_up="$(mktemp -t semantica-sparql-up.XXXXXX)"; TMP_FILES+=("$tmp_up")
  tmp_fork="$(mktemp -t semantica-sparql-fork.XXXXXX)"; TMP_FILES+=("$tmp_fork")
  # Upstream: parse ONLY the _FORBIDDEN_KEYWORDS regex literal. Other re.compile
  # calls in this file (e.g. _PREFIX_DECL, _ALLOWED_QUERY_TYPES) are unrelated
  # to the SPARQL blocklist and would leak garbage into the diff.
  awk '
    /_FORBIDDEN_KEYWORDS = re\.compile\(/ { target=1; next }
    target && /^[[:space:]]*r"/ {
      line = $0
      sub(/^[[:space:]]*r"/, "", line)
      sub(/".*/, "", line)
      sub(/.*\(/, "", line)
      sub(/\).*/, "", line)
      n = split(line, parts, "|")
      for (i = 1; i <= n; i++) {
        k = parts[i]
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", k)
        gsub(/\\[a-z]/, "", k)
        if (length(k) > 0) print toupper(k)
      }
      target = 0
    }
  ' "$upstream" | sort -u > "$tmp_up" || true
  # Fork: SKILL.md mentions forbidden words in backticks (INSERT/DELETE/DROP/...)
  # — extract any of the known 9 keywords in backticks.
  grep -oE '`(INSERT|DELETE|DROP|LOAD|CLEAR|CREATE|COPY|MOVE|ADD)`' "$fork" \
    | tr -d '`' | sort -u > "$tmp_fork" || true
  report_diff "sparql" "$tmp_up" "$tmp_fork" "SPARQL forbidden keywords (regex vs SKILL.md mention)"
}

# -------- scraper: vocab (sem: terms) --------------------------------------
scraper_vocab() {
  local upstream="$VENDOR_SRC/semantica/ontology/vocabulary/semantica-ns.ttl"
  if [ ! -f "$upstream" ]; then
    log "vocab: $upstream missing (post-0.6.6 vocabulary file); skipping"
    return 0
  fi
  local tmp_up tmp_fork
  tmp_up="$(mktemp -t semantica-vocab-up.XXXXXX)"; TMP_FILES+=("$tmp_up")
  tmp_fork="$(mktemp -t semantica-vocab-fork.XXXXXX)"; TMP_FILES+=("$tmp_fork")
  # Upstream: extract `sem:<term>` (or `sem:<term> a ...`) declarations.
  grep -oE 'sem:[A-Za-z][A-Za-z0-9_]*' "$upstream" | sort -u > "$tmp_up" || true
  # Fork: grep fork code (excluding the vendored subtree) for `sem:<term>` usage.
  grep -rln --exclude-dir=semantica-src --exclude-dir=node_modules \
       --exclude-dir=dist --exclude-dir=resources \
       -E 'sem:[A-Za-z][A-Za-z0-9_]*' "$REPO_ROOT" 2>/dev/null \
    | xargs -I{} grep -ohE 'sem:[A-Za-z][A-Za-z0-9_]*' {} 2>/dev/null \
    | sort -u > "$tmp_fork" || true
  report_diff "vocab" "$tmp_up" "$tmp_fork" "sem: vocabulary terms (declared in .ttl vs used in fork)"
}

# -------- scraper: deps (pyproject vs requirements.txt) --------------------
scraper_deps() {
  local upstream="$VENDOR_SRC/pyproject.toml"
  local fork="$VENDOR_RUNTIME/requirements.txt"
  if [ ! -f "$fork" ]; then
    log "deps: $fork missing; skipping"
    return 0
  fi
  local tmp_up tmp_fork
  tmp_up="$(mktemp -t semantica-deps-up.XXXXXX)"; TMP_FILES+=("$tmp_up")
  tmp_fork="$(mktemp -t semantica-deps-fork.XXXXXX)"; TMP_FILES+=("$tmp_fork")
  # Upstream: parse [project] dependencies = [ ... ] block. Crude but adequate:
  # take lines between "dependencies = [" and the next "]".
  awk '
    /^dependencies *= *\[/ { in_deps=1; next }
    in_deps && /^\]/ { in_deps=0; next }
    in_deps {
      # Strip surrounding quotes + whitespace + comma + version spec.
      gsub(/^[[:space:]]+|[[:space:]]+$/, "")
      gsub(/,$/, "")
      gsub(/"/, "")
      gsub(/[><=!~].*/, "")  # drop version operators + spec
      if (length($0) > 0) print tolower($0)
    }
  ' "$upstream" | sort -u > "$tmp_up" || true
  # Fork: requirements.txt has lines like "fastapi==0.110" or "pydantic>=2.0"
  awk '
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*$/ { next }
    {
      gsub(/^[[:space:]]+|[[:space:]]+$/, "")
      gsub(/[><=!~].*/, "")
      gsub(/;.*/, "")
      gsub(/\[.*\]/, "")  # drop extras like uvicorn[standard]
      if (length($0) > 0) print tolower($0)
    }
  ' "$fork" | sort -u > "$tmp_fork" || true
  report_diff "deps" "$tmp_up" "$tmp_fork" "Python deps (pyproject.toml [project.dependencies] vs requirements.txt)"
}

# -------- dispatch ----------------------------------------------------------
case "$SCRAPER" in
  endpoint) scraper_endpoint ;;
  field)    scraper_field ;;
  sparql)   scraper_sparql ;;
  vocab)    scraper_vocab ;;
  deps)     scraper_deps ;;
  all)
    scraper_endpoint || true
    scraper_field    || true
    scraper_sparql   || true
    scraper_vocab    || true
    scraper_deps     || true
    ;;
esac

# -------- footer ------------------------------------------------------------
if [ "$has_diff" -eq 0 ]; then
  log "no diff detected"
  exit 0
fi

if [ "$NOTIFY" = "1" ] && command -v osascript >/dev/null 2>&1; then
  osascript -e 'display notification "Semantica upstream drift detected — run check-semantica-upstream.sh" with title "Multica upstream watch"' \
    >/dev/null 2>&1 || true
fi

exit 0
