#!/usr/bin/env bash
# scripts/snapshot-semantica-graph.sh (0.5.28 Semantica × Multica P1-3-half)
# -----------------------------------------------------------------------------
# Snapshot the Multica Semantica knowledge-graph JSON + .provenance SQLite
# into the current pre-update backup directory. Excludes *.api-key (the
# 0600 secret Semantica mints at run.sh:158-161) — a future glob slip
# cannot leak the key into a tarball readable by other users.
#
# Synthesizer Round 7 (0.5.28): the audit found pre-update-snapshot.sh had
# no graph*.json coverage; 0.5.27 cold-launch with semantica enabled
# would lose every issue's decision history on .app upgrade. This script
# closes that gap; ~/.multica/scripts/pre-update-snapshot.sh source-includes
# this file once the user copies the ship note into their backup recipe.
#
# Usage (manual wire-in to ~/.multica/scripts/pre-update-snapshot.sh Step 3
# area — after the existing profiles/ rsync but before the git-tag Step 5):
#
#     export BACKUP_DIR="$BACKUP_DIR"
#     source "$REPO_ROOT/scripts/snapshot-semantica-graph.sh"
#
# Defense-in-depth:
#   - umask 077 so a tar extract on a future machine writes 0600, not 0644.
#   - --exclude='*.api-key' is a belt-and-braces match against the basename,
#     not the path — catches the Semantica upstream's
#     `${GRAPH_PATH}.api-key` file under either the legacy root path
#     (~/.multica/semantica-graph.json.api-key, 0.5.27) or the workspace-
#     scoped path (0.5.29 lands: ~/.multica/workspaces/<wsId>/semantica-
#     graph.json.api-key).
#   - nullglob so the script exits 0 when no graph files exist (fresh
#     installs, semantica flag never enabled). Not an error.
#
# Exit codes:
#   0  tarball written OR no graph files (silent skip)
#   1  BACKUP_DIR unset (caller bug)
#   2  tar failed for a non-empty match set (caller-visible)
# -----------------------------------------------------------------------------
set -euo pipefail

: "${BACKUP_DIR:?BACKUP_DIR must be set (caller sets it before sourcing)}"

# Block accidental direct execution (the script is meant to be sourced so it
# inherits the caller's BACKUP_DIR). Direct execution is a developer
# convenience: BACKUP_DIR is honored, output still goes to stderr.
SOURCED=0
if [ "${BASH_SOURCE[0]:-}" != "$0" ]; then
  SOURCED=1
fi

umask 077

TARBALL="$BACKUP_DIR/multica-semantica-graph.tgz"

# Collect files via find (NUL-separated). Avoids the nullglob-vs-array
# expansion-timing trap: GLOB_FILES=("$pattern") stores the literal pattern,
# and ${arr[@]} expansion at use-time is non-deterministic across bash
# versions when nullglob is toggled inside the same script. find is POSIX,
# portable, and gives a stable NUL-delimited stream regardless of names
# containing spaces or unicode.
mapfile -d '' GLOB_FILES < <(find \
  "${HOME}/.multica" \
  -maxdepth 3 \
  \( -name 'semantica-graph*.json' -o -name 'semantica-graph*.provenance' \) \
  -not -name '*.api-key' \
  -print0 2>/dev/null || true)

if [ ${#GLOB_FILES[@]} -eq 0 ]; then
  echo "[semantica-snapshot] no graph files found; skip (not an error)" >&2
  exit 0
fi

if tar czf "$TARBALL" --exclude='*.api-key' "${GLOB_FILES[@]}" 2>/dev/null; then
  echo "[semantica-snapshot] -> $TARBALL ($(du -h "$TARBALL" | cut -f1), ${#GLOB_FILES[@]} files)" >&2
  exit 0
fi

# tar exits non-zero on partial errors; treat no-match-already-handled above
# as the only non-error path. Anything else bubbles up.
echo "[semantica-snapshot] WARN: tar failed for $TARBALL (caller-visible)" >&2
exit 2