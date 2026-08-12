---
name: incident-2026-08-12-backup-sh-overwrite-recovery
created: 2026-08-12T13:31:34Z
updated: 2026-08-12T13:31:34Z
type: incident
status: closed
severity: P1
ship: 0.5.17
---

# Incident: `scripts/backup.sh` overwrite recovery — 3 features lost, full revert, surgical re-apply (2026-08-12)

**Date**: 2026-08-12 (during 0.5.17 ship hygiene continuation)
**Severity**: P1 (concrete data loss in a tracked script — features silently
dropped, only caught by diff-stat self-audit, no production deployment yet)
**Versions affected**: working tree only — never landed on a release; the
bad commit (`387facab9`) was reverted in the same session before any
ship.

## Summary

While implementing the 0.5.17 lesson #2 follow-up (wire the local
`.omc/backups/` snapshot into `ship-mac.sh` step 6b), the working-tree
edit overwrote the **prior 261-line `scripts/backup.sh` implementation
from `fe6e7c83f`** with a 150-line rewrite that omitted three features
the prior version already shipped:

1. `--dry-run` (declare / parse / plan-only exit path)
2. Kebab-case `--reason` regex with version dots allowed
   (`^[a-z0-9][a-z0-9.\-]{1,63}$` — the canonical case being `0.5.17-ship`)
3. `--notes-file <path>` (optional notes capture into the backup
   `notes.md`)

The bad commit landed as `387facab9` ("`chore(ship): wire local
.omc/backups/ snapshot into ship-mac.sh step 6b`"). The diff-stat
showed **1 file changed, 104 insertions(+), 214 deletions(-)** —
a 214-line deletion against a 261-line file is an anomaly that should
have stopped the change on its own, but it slipped past initial review.

## The three lost features (with file:line refs in `fe6e7c83f:scripts/backup.sh`)

| # | Feature | Baseline file:line |
|---|---|---|
| 1 | `--dry-run` (declare / parse / plan-only exit) | usage: line 21 (`# bash scripts/backup.sh --reason foo --trigger release --dry-run    # show plan, no write`); variable: line 51 (`DRY_RUN=false`); arg parse: line 65 (`--dry-run) DRY_RUN=true; shift ;;`); gate: line 162 (`if $DRY_RUN; then echo "DRY-RUN — no files written"; rm -f "$FILES_LIST"; exit 0; fi`) |
| 2 | Kebab-case `--reason` regex with version dots allowed (`^[a-z0-9][a-z0-9.\-]{1,63}$`) | line 76 (`if ! [[ "$REASON" =~ ^[a-z0-9][a-z0-9.\-]{1,63}$ ]]; then`) — the canonical case being the `0.5.17-ship` slug the new step 6b itself needs to pass |
| 3 | `--notes-file <path>` (optional notes into `notes.md`) | usage: line 24 (`bash scripts/backup.sh --reason epic-rewrite --trigger manual --notes-file /tmp/notes.md`); variable: line 52 (`NOTES_FILE=""`); arg parse: line 63 (`--notes-file) NOTES_FILE="$2"; shift 2 ;;`); copy block: lines 224-230 (`if [[ -n "$NOTES_FILE" ]]; then if [[ -f "$NOTES_FILE" ]]; then cp "$NOTES_FILE" "$TARGET/notes.md"; ...`) |

These three were not nice-to-haves:

- `--dry-run` is the **safe-by-default** mode for the new step 6b —
  without it the ship chain has no way to preview the backup it is
  about to write before committing the version slug.
- The regex change is **load-bearing for the new step 6b**: the slug
  `0.5.17-ship` contains a `.` (the version dot). A regex that
  drops the `\.` in the char class rejects the canonical reason
  string step 6b itself emits — meaning the bad commit would have
  broken its own caller.
- `--notes-file` is what the ship gate will need to attach a release
  note (`release-notes-<ver>.md`) to each backup; without it, every
  shipped backup is unannotated.

## The detection trigger: 214-deletions anomaly

`git show --stat 387facab9 -- scripts/backup.sh` reported:

```
 scripts/backup.sh | 318 ++++++++++++++++++------------------------------------
 1 file changed, 104 insertions(+), 214 deletions(-)
```

A 104-insert / 214-delete split against a 261-line file (≈82% rewrite)
is a **structural red flag**, not a normal edit. The bad commit landed
under the radar because the parent diff that motivated it (`scripts/ship-mac.sh`
+25 lines, surgical scope) was the *correct* shape — but the working-tree
state for `scripts/backup.sh` got swept up in the same edit and was not
isolated. Self-audit caught the anomaly during pre-commit inspection;
reported to the user, who chose "完全 revert" (`git revert 387facab9`
→ `67d6b4e1b`).

## Recovery (3-commit sequence)

| Order | Commit | What |
|---|---|---|
| 1 (baseline) | `fe6e7c83f` | Prior 261-line `scripts/backup.sh` implementation (had all 3 features) |
| 2 (bad) | `387facab9` | `chore(ship): wire local .omc/backups/ snapshot into ship-mac.sh step 6b` — rewrote `scripts/backup.sh` (104 / -214) AND added `scripts/ship-mac.sh` (+25) |
| 3 (revert) | `67d6b4e1b` | `Revert "chore(ship): wire local .omc/backups/ snapshot into ship-mac.sh step 6b"` — full revert (restored both files to their pre-`387facab9` state) |
| 4 (corrected) | `fce5a5b9c` | Same intent, surgical scope: **only `scripts/ship-mac.sh`** (net +25 lines, +25 insertions). Commit message explicitly states "Does NOT touch `scripts/backup.sh` (its prior implementation in `fe6e7c83f` already implements `.omc/backups/README.md` contract ...)". `scripts/backup.sh` stayed at the `fe6e7c83f` 261-line baseline. |

User's recovery decision: **"完全 revert"** (full revert, no partial
recovery). After the revert, re-applied the desired change with
**scoped intent only** — `scripts/ship-mac.sh` step 6b, leaving the
already-correct `scripts/backup.sh` alone.

## Pattern: verify-before-fix applies to coding tasks, not just audits

This is the literal live execution of the audit-false-positive
pattern from `.omc/incidents/2026-08-12-0.5.17-audit-false-positive.md`
— same root cause, different shape:

- **Audit-false-positive pattern**: an audit item said X was missing;
  agent was about to "fix" X; verify-before-fix step proved X already
  existed (3 cases: `matchLocale` collapses zh-Hant by design,
  `LabChatPanel` already at `lab-chat-panel.tsx`, `DEFAULT_TABS`
  already 6 kinds).
- **This incident**: a working-tree edit was about to overwrite a
  file whose **prior implementation already shipped the feature**;
  verify-before-fix should have caught "the feature I am adding
  already exists in the prior file" before deleting 214 lines.

Both share the failure mode: **assume the gap exists → write code
to close it → land a regression to fix an imaginary problem**.

The fix in both cases is identical and small:

1. **Before editing an existing file**, run `git show <prior-implementation-commit>:path/to/file`
   and `wc -l` on it. If the file is already substantial, read at
   least the function/arg-parse section you are about to "add" —
   check whether the surface you are wiring up already exists.
2. **Before claiming a new flag/regex/option**, grep the file for
   the symbol. `grep -n "DRY_RUN\|--dry-run\|--notes-file\|kebab-case"`
   would have surfaced all three lost features in one pass.
3. **Before declaring "scoped intent"**, the diff-stat's insert/delete
   ratio against the file size is the cheapest sanity check.
   `318 lines changed, 104 insertions, 214 deletions, on a 261-line
   file` is a structural red flag — never a routine edit.

The `Known Stability Surfaces` section in `CLAUDE.md` already has
the audit-side lesson; this incident extends it to coding edits.

## Lesson locked into CLAUDE.md (0.5.17)

The `Known Stability Surfaces` section gains:
> **Verify-before-fix applies to coding edits, not just audits
> (0.5.17 follow-on lesson, `387facab9`).** A "wire the backup
> into ship-mac.sh" task overwrote `scripts/backup.sh` (261-line
> baseline in `fe6e7c83f`) with a 150-line rewrite that dropped
> `--dry-run`, the kebab-case regex with version dots allowed
> (`^[a-z0-9][a-z0-9.\-]{1,63}$`), and `--notes-file`. The
> 214-deletions stat against a 261-line file is a structural red
> flag, never a routine edit. Before touching an existing file,
> read the prior implementation, grep for the symbol you are
> about to "add", and check the insert/delete ratio against the
> file size. Same root cause as the 3 audit false positives —
> `audit-false-positive.md` — applied to the working tree instead
> of the audit queue. Pattern: **if the feature already exists in
> the prior file, do not rewrite — only add the new caller**.

## Related

- Sibling incident (same root cause, audit side):
  `.omc/incidents/2026-08-12-0.5.17-audit-false-positive.md`
- 0.5.17 ship log: `.omc/0.5.17-ship-2026-08-12.md` (Lessons #1)
- Predecessor pattern: memory `0.5.17-lab-usability-2026-08-12.md`
- Recovery commits: `67d6b4e1b` (full revert), `fce5a5b9c` (surgical re-apply, `scripts/ship-mac.sh` only)
- Bad commit (do not rebase / cherry-pick): `387facab9`
- Baseline (reference): `fe6e7c83f`