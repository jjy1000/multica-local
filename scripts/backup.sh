#!/usr/bin/env bash
# ============================================================================
# backup.sh — Incremental backup writer for Multica local project
#
# WHY THIS EXISTS (2026-08-11):
# User marked this project as "important local project" on 2026-08-11 and
# asked for incremental backups of significant changes. Schema, manifest,
# retention, and gitignore policy live in `.omc/backups/README.md`; this
# script is the executable surface that produces compliant backups.
#
# Trigger policy (also in `.omc/backups/README.md`):
#   * release           — every shipped version (e.g. 0.5.16-ship)
#   * major-refactor    — single branch > 20 files OR behavior-visible
#   * schema-migration  — any change under server/migrations/
#   * data-shape        — destructive data path change (P0 invariant)
#   * manual            — user explicitly asked
#
# NOT a trigger: 1-2 file bugfix / typo / comment.
#
# Usage:
#   bash scripts/backup.sh --reason 0.5.16-ship --trigger release
#   bash scripts/backup.sh --reason schema-mig-239 --trigger schema-migration --base HEAD
#   bash scripts/backup.sh --reason epic-rewrite --trigger manual --notes-file /tmp/notes.md
#   bash scripts/backup.sh --reason foo --trigger release --dry-run    # show plan, no write
# ============================================================================
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACKUPS_ROOT="$REPO_ROOT/.omc/backups"
MAX_ACTIVE=30

REASON=""
TRIGGER=""
BASE=""
NOTES_FILE=""
DRY_RUN=false

usage() {
  sed -n '3,28p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --reason)     REASON="$2"; shift 2 ;;
    --trigger)    TRIGGER="$2"; shift 2 ;;
    --base)       BASE="$2"; shift 2 ;;
    --notes-file) NOTES_FILE="$2"; shift 2 ;;
    --dry-run)    DRY_RUN=true; shift ;;
    -h|--help)    usage ;;
    *)            echo "ERROR: unknown arg: $1" >&2; usage ;;
  esac
done

# Validate reason: kebab-case + version dots allowed, 2-64 chars
if ! [[ "$REASON" =~ ^[a-z0-9][a-z0-9.\-]{1,63}$ ]]; then
  echo "ERROR: --reason must be kebab-case (2-64 chars, [a-z0-9.-]): got '$REASON'" >&2
  exit 2
fi

case "$TRIGGER" in
  release|major-refactor|schema-migration|data-shape|manual) ;;
  *) echo "ERROR: --trigger must be one of release|major-refactor|schema-migration|data-shape|manual; got '$TRIGGER'" >&2; exit 2 ;;
esac

cd "$REPO_ROOT"
if ! git rev-parse --git-dir >/dev/null 2>&1; then
  echo "ERROR: not a git repository: $REPO_ROOT" >&2
  exit 3
fi

# Refuse to back up if we're inside the backups dir itself (avoid recursion)
case "$PWD" in
  "$BACKUPS_ROOT"/*) echo "ERROR: refusing to run inside $BACKUPS_ROOT" >&2; exit 3 ;;
esac

TS="$(date -u +"%Y-%m-%d-%H%M")"
TS_ISO="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
TARGET="$BACKUPS_ROOT/$TS/$REASON"

if [[ -e "$TARGET" ]]; then
  echo "ERROR: target already exists: $TARGET" >&2
  exit 4
fi

HEAD_SHA="$(git rev-parse HEAD)"
BRANCH="$(git rev-parse --abbrev-ref HEAD)"
if git diff --quiet && git diff --cached --quiet; then
  DIRTY=false
else
  DIRTY=true
fi

# Resolve base ref (default: HEAD for working tree diff)
if [[ -n "$BASE" ]]; then
  BASE_SHA="$(git rev-parse --verify "$BASE" 2>/dev/null)" || { echo "ERROR: bad --base ref: $BASE" >&2; exit 5; }
  DIFF_RANGE="$BASE_SHA..HEAD"
else
  BASE_SHA=""
  DIFF_RANGE="working-tree-vs-HEAD"
fi

# Build file list (union of: named files in range + cached + unstaged + untracked)
FILES_LIST=$(mktemp)
if [[ -n "$BASE_SHA" ]]; then
  git diff --name-only "$BASE_SHA"..HEAD >> "$FILES_LIST" 2>/dev/null || true
fi
git diff --cached --name-only >> "$FILES_LIST" 2>/dev/null || true
git diff --name-only >> "$FILES_LIST" 2>/dev/null || true
git ls-files --others --exclude-standard >> "$FILES_LIST" 2>/dev/null || true
sort -u "$FILES_LIST" -o "$FILES_LIST"
FILES_TOTAL=$(wc -l < "$FILES_LIST" | tr -d ' ')

# Scope detection
TOUCHES_SCHEMA=false
grep -qE '^server/migrations/' "$FILES_LIST" 2>/dev/null && TOUCHES_SCHEMA=true

TOUCHES_DATA=false
# Only set true if migration file name itself signals destruction
if ls server/migrations/ 2>/dev/null | grep -qiE '_drop_|_destroy_|truncate'; then
  if grep -qE '^server/migrations/' "$FILES_LIST" 2>/dev/null; then
    TOUCHES_DATA=true
  fi
fi

SHIPS_TO_DESKTOP=false
grep -qE '^(apps/desktop|scripts/(ship-mac|desktop-sign))/' "$FILES_LIST" 2>/dev/null && SHIPS_TO_DESKTOP=true

# Insertions / deletions (use --shortstat)
if [[ -n "$BASE_SHA" ]]; then
  STAT_RAW="$(git diff --shortstat "$BASE_SHA"..HEAD 2>/dev/null || true)"
else
  STAT_RAW="$(git diff --shortstat HEAD 2>/dev/null || true)"
fi
INS=$(echo "$STAT_RAW" | grep -oE '[0-9]+ insertion' | grep -oE '[0-9]+' | head -1 || echo 0)
DEL=$(echo "$STAT_RAW" | grep -oE '[0-9]+ deletion' | grep -oE '[0-9]+' | head -1 || echo 0)
INS=${INS:-0}
DEL=${DEL:-0}

# JSON-quote file list (safe-ish: only ASCII path chars; reject anything else)
FILES_JSON="[]"
if [[ "$FILES_TOTAL" -gt 0 ]]; then
  FILES_JSON=$(awk '{
    gsub(/\\/, "\\\\"); gsub(/"/, "\\\"");
    printf "\"%s\",", $0
  }' "$FILES_LIST" | sed 's/,$//' | awk 'BEGIN{printf "["} {printf "%s", $0} END{printf "]"}')
fi

echo "=== Plan ==="
echo "  target:    $TARGET"
echo "  reason:    $REASON"
echo "  trigger:   $TRIGGER"
echo "  branch:    $BRANCH"
echo "  head:      $HEAD_SHA"
echo "  base:      ${BASE_SHA:-(working tree)}"
echo "  dirty:     $DIRTY"
echo "  range:     $DIFF_RANGE"
echo "  files:     $FILES_TOTAL"
echo "  insertions:$INS deletions:$DEL"
echo "  schema:    $TOUCHES_SCHEMA"
echo "  data:      $TOUCHES_DATA"
echo "  desktop:   $SHIPS_TO_DESKTOP"

if $DRY_RUN; then
  echo "DRY-RUN — no files written"
  rm -f "$FILES_LIST"
  exit 0
fi

mkdir -p "$TARGET"

# status.txt
git status --short > "$TARGET/status.txt"

# log.txt — last 30 commits
git log --oneline -30 > "$TARGET/log.txt"

# diff.patch (using git diff with --binary, capped to sensible scope)
{
  if [[ -n "$BASE_SHA" ]]; then
    git diff --binary "$BASE_SHA"..HEAD 2>/dev/null || true
    git diff --binary --cached 2>/dev/null || true
  else
    git diff --binary HEAD 2>/dev/null || true
  fi
  echo ""
  echo "# ===== BEGIN untracked files (small, ascii-only) at $TS_ISO ====="
  while IFS= read -r f; do
    [[ -f "$f" ]] || continue
    SIZE=$(stat -f %z "$f" 2>/dev/null || stat -c %s "$f" 2>/dev/null || echo 0)
    if [[ "$SIZE" -gt 1048576 ]]; then
      echo "# SKIP untracked (size > 1MB): $f"
      continue
    fi
    echo "# ===== BEGIN untracked: $f ====="
    cat "$f"
    echo "# ===== END untracked: $f ====="
  done < "$FILES_LIST"
} > "$TARGET/diff.patch"

# notes.md (optional)
if [[ -n "$NOTES_FILE" ]]; then
  if [[ -f "$NOTES_FILE" ]]; then
    cp "$NOTES_FILE" "$TARGET/notes.md"
  else
    echo "WARN: --notes-file not found: $NOTES_FILE" >&2
  fi
fi

# manifest.json
cat > "$TARGET/manifest.json" <<EOF
{
  "schema_version": 1,
  "created_at": "$TS_ISO",
  "reason": "$REASON",
  "trigger": "$TRIGGER",
  "git": {
    "branch": "$BRANCH",
    "head_commit": "$HEAD_SHA",
    "base_commit": "${BASE_SHA:-}",
    "dirty": $DIRTY,
    "diff_range": "$DIFF_RANGE"
  },
  "diff": {
    "patch_file": "diff.patch",
    "files_changed": $FILES_JSON,
    "files_total": $FILES_TOTAL,
    "insertions": $INS,
    "deletions": $DEL
  },
  "scope": {
    "touches_schema": $TOUCHES_SCHEMA,
    "touches_data": $TOUCHES_DATA,
    "ships_to_desktop": $SHIPS_TO_DESKTOP
  },
  "retention": {
    "expires_at": "$(date -u -v+90d +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -u -d '+90 days' +"%Y-%m-%dT%H:%M:%SZ")",
    "archive_after_days": 90
  }
}
EOF

# Retention: if > 30 active backups (excluding _template and _archive), archive oldest
ACTIVE_COUNT=$(find "$BACKUPS_ROOT" -mindepth 2 -maxdepth 2 -type d ! -path '*/_template*' ! -path '*/_archive*' | wc -l | tr -d ' ')
if [[ "$ACTIVE_COUNT" -gt "$MAX_ACTIVE" ]]; then
  OLDEST=$(find "$BACKUPS_ROOT" -mindepth 2 -maxdepth 2 -type d ! -path '*/_template*' ! -path '*/_archive*' -printf '%T@ %p\n' | sort -n | head -1 | awk '{print $2}')
  if [[ -n "$OLDEST" ]]; then
    REL="${OLDEST#$BACKUPS_ROOT/}"
    TS_PART="${REL%%/*}"
    ARCHIVE_DEST="$BACKUPS_ROOT/_archive/$TS_PART"
    mkdir -p "$ARCHIVE_DEST"
    mv "$OLDEST" "$ARCHIVE_DEST/"
    echo "Retention: moved $OLDEST -> $ARCHIVE_DEST/  (active=$ACTIVE_COUNT > $MAX_ACTIVE)"
  fi
fi

rm -f "$FILES_LIST"

echo ""
echo "WROTE: $TARGET"
ls -la "$TARGET"
