#!/usr/bin/env bash
# ============================================================================
# backup.sh — .omc/backups/ 增量历史备份 (per local-project-backup-protocol)
#
# Creates .omc/backups/<TS>/<reason>/ with manifest + diff + status + log.
# Never touches the working tree; never reverts. Pure capture.
#
# Usage:
#   bash scripts/backup.sh --reason 0.5.18-ship --trigger release
#   bash scripts/backup.sh --reason schema-mig-239 --trigger schema-migration --base HEAD~3
#   bash scripts/backup.sh --reason epic-rewrite --trigger manual --notes "refactor agent list page"
#
# Conventions (.omc/backups/README.md):
#   <TS>      = YYYY-MM-DD-HHMM (UTC)
#   <reason>  = kebab-case slug
#   Active    = .omc/backups/                       (latest 30)
#   Archive   = .omc/backups/_archive/<TS>/<reason> (overflow)
# ============================================================================
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACKUP_ROOT="$REPO_ROOT/.omc/backups"
ACTIVE_LIMIT=30
ARCHIVE_AFTER_DAYS=90

REASON=""
TRIGGER="manual"
BASE=""
NOTES=""

usage() {
  cat <<EOF
Usage: bash scripts/backup.sh --reason <slug> [--trigger release|major-refactor|schema-migration|data-shape|manual] [--base <ref>] [--notes "<text>"]
EOF
  exit 64
}

while [ $# -gt 0 ]; do
  case "$1" in
    --reason)  REASON="$2"; shift 2 ;;
    --trigger) TRIGGER="$2"; shift 2 ;;
    --base)    BASE="$2"; shift 2 ;;
    --notes)   NOTES="$2"; shift 2 ;;
    -h|--help) usage ;;
    *)         echo "unknown flag: $1" >&2; usage ;;
  esac
done

[ -n "$REASON" ] || { echo "--reason is required" >&2; usage; }
[[ "$REASON" =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] || { echo "--reason must be kebab-case" >&2; exit 65; }

# --- 1. confirm git repo + branch -------------------------------------------
cd "$REPO_ROOT"
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || { echo "not a git repo: $REPO_ROOT" >&2; exit 66; }
BRANCH="$(git branch --show-current)"
HEAD_SHA="$(git rev-parse HEAD)"

# --- 2. <TS> + active/archive directory --------------------------------------
TS="$(date -u +"%Y-%m-%d-%H%M")"
DEST="$BACKUP_ROOT/$TS/$REASON"
mkdir -p "$DEST"

# --- 3. write manifest.json --------------------------------------------------
SCHEMA_VERSION=1
CREATED_AT="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
STATUS_SNAPSHOT="$(git status --short | head -200 || true)"
DIFF_STAT="$(git diff --stat ${BASE:+${BASE}..}HEAD | tail -1 || true)"
INSERTIONS="$(echo "$DIFF_STAT" | awk '{print $4+0}' 2>/dev/null || echo 0)"
DELETIONS="$(echo "$DIFF_STAT" | awk '{print $6+0}' 2>/dev/null || echo 0)"
FILES_CHANGED_JSON="$(git diff --name-only ${BASE:+${BASE}..}HEAD | python3 -c 'import json,sys; print(json.dumps([l.strip() for l in sys.stdin if l.strip()]))' 2>/dev/null || echo '[]')"
DIRTY="$(git status --short | grep -q . && echo true || echo false)"
TOUCHES_SCHEMA="$(git diff --name-only ${BASE:+${BASE}..}HEAD | grep -qE '^server/migrations/.*\.sql$' && echo true || echo false)"
TOUCHES_DATA="$(git diff --name-only ${BASE:+${BASE}..}HEAD | grep -qE '(experimental|destructive|drop|truncate)' && echo true || echo false)"

cat > "$DEST/manifest.json" <<EOF
{
  "schema_version": $SCHEMA_VERSION,
  "created_at": "$CREATED_AT",
  "reason": "$REASON",
  "trigger": "$TRIGGER",
  "git": {
    "branch": "$BRANCH",
    "head_commit": "$HEAD_SHA",
    "base_commit": "${BASE:-}",
    "dirty": $DIRTY,
    "status_snapshot": $(printf '%s' "$STATUS_SNAPSHOT" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))')
  },
  "diff": {
    "patch_file": "diff.patch",
    "files_changed": $FILES_CHANGED_JSON,
    "insertions": $INSERTIONS,
    "deletions": $DELETIONS,
    "stat": $(printf '%s' "$DIFF_STAT" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read().strip()))')
  },
  "scope": {
    "files_total": $(echo "$FILES_CHANGED_JSON" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))' 2>/dev/null || echo 0),
    "touches_schema": $TOUCHES_SCHEMA,
    "touches_data": $TOUCHES_DATA,
    "ships_to_desktop": $([ "$TRIGGER" = "release" ] && echo true || echo false)
  },
  "notes_md": $([ -n "$NOTES" ] && echo "\"notes.md\"" || echo "null"),
  "verification": {
    "typecheck_passed": null,
    "go_test_passed": null,
    "manual_smoke_passed": null,
    "evidence": null
  },
  "retention": {
    "expires_at": "$(date -u -v+${ARCHIVE_AFTER_DAYS}d +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -u -d "+${ARCHIVE_AFTER_DAYS} days" +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || echo "unknown")",
    "archive_after_days": $ARCHIVE_AFTER_DAYS
  }
}
EOF

# --- 4. write diff.patch ------------------------------------------------------
git diff --binary ${BASE:+${BASE}..}HEAD > "$DEST/diff.patch" || true

# --- 5. write status.txt + log.txt -------------------------------------------
git status --short > "$DEST/status.txt"
git log --oneline -20 > "$DEST/log.txt"

# --- 6. write notes.md (if provided) -----------------------------------------
if [ -n "$NOTES" ]; then
  cat > "$DEST/notes.md" <<EOF
# Backup notes — $REASON ($TS)

- **Created**: $CREATED_AT
- **Branch**: $BRANCH
- **HEAD**: $HEAD_SHA
- **Trigger**: $TRIGGER
- **Base**: ${BASE:-HEAD}

$NOTES
EOF
fi

# --- 7. rotate: if active > 30, move oldest to _archive/ ---------------------
ACTIVE_COUNT=$(find "$BACKUP_ROOT" -maxdepth 2 -mindepth 2 -type d ! -name '_archive' ! -name '_template' | wc -l | tr -d ' ')
if [ "$ACTIVE_COUNT" -gt "$ACTIVE_LIMIT" ]; then
  OLDEST=$(find "$BACKUP_ROOT" -maxdepth 2 -mindepth 2 -type d ! -name '_archive' ! -name '_template' -printf '%T@ %p\n' | sort -n | head -1 | awk '{print $2}')
  REL="${OLDEST#$BACKUP_ROOT/}"
  mkdir -p "$BACKUP_ROOT/_archive"
  git mv "$OLDEST" "$BACKUP_ROOT/_archive/$REL" 2>/dev/null || mv "$OLDEST" "$BACKUP_ROOT/_archive/$REL"
  echo "  rotated: $REL -> _archive/"
fi

# --- 8. output ----------------------------------------------------------------
echo "backup written: .omc/backups/$TS/$REASON/"
echo "  files: $(find "$DEST" -type f | wc -l | tr -d ' ')"
echo "  diff:  $(wc -l < "$DEST/diff.patch" 2>/dev/null || echo 0) lines"
echo "  insertions=$INSERTIONS  deletions=$DELETIONS"